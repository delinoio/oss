use std::{future::pending, time::Duration};

use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
    time::timeout,
};

use super::*;

#[tokio::test]
async fn slow_native_trust_initialization_survives_attempt_cancellation() {
    use std::sync::{
        atomic::{AtomicUsize, Ordering},
        mpsc,
    };

    let client = HttpClient::default();
    let starts = Arc::new(AtomicUsize::new(0));
    let observed_starts = starts.clone();
    let (release, blocked) = mpsc::channel();
    assert!(timeout(
        Duration::from_millis(10),
        client.get_or_init(move || {
            observed_starts.fetch_add(1, Ordering::SeqCst);
            blocked.recv().map_err(|_| Code::TrustStore)?;
            http_client(None)
        })
    )
    .await
    .is_err());
    for _ in 0..3 {
        assert!(timeout(
            Duration::from_millis(10),
            client.get_or_init(|| { panic!("a retry must not start another native trust load") })
        )
        .await
        .is_err());
    }
    release.send(()).unwrap();
    timeout(
        Duration::from_secs(2),
        client.get_or_init(|| panic!("the original native trust load must remain available")),
    )
    .await
    .unwrap()
    .unwrap();
    client
        .get_or_init(|| panic!("the completed client must be cached"))
        .await
        .unwrap();
    assert_eq!(starts.load(Ordering::SeqCst), 1);
}

#[tokio::test]
async fn all_addresses_get_a_chance_and_successful_tcp_connection_is_closed() {
    let addresses = vec!["127.0.0.1:1".parse().unwrap(), "[::1]:2".parse().unwrap()];
    let result = timeout(
        Duration::from_millis(100),
        connect_addresses(addresses, |addr| async move {
            if addr.port() == 1 {
                pending().await
            } else {
                Ok(42)
            }
        }),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(result, 42);
    for bind in ["127.0.0.1:0", "[::1]:0"] {
        let listener = TcpListener::bind(bind).await.unwrap();
        let addr = listener.local_addr().unwrap();
        let target = Target::Tcp(TcpTarget {
            host: addr.ip().to_string(),
            port: addr.port(),
        });
        target.check().await.unwrap();
        let (mut stream, _) = listener.accept().await.unwrap();
        assert_eq!(
            timeout(Duration::from_secs(2), stream.read(&mut [0; 1]))
                .await
                .unwrap()
                .unwrap(),
            0
        );
    }
}

#[tokio::test]
async fn terminal_address_failure_does_not_discard_another_success() {
    let addresses = vec!["[::1]:1".parse().unwrap(), "127.0.0.1:2".parse().unwrap()];
    let result = connect_addresses(addresses, |addr| async move {
        if addr.port() == 1 {
            Err(io::Error::from(io::ErrorKind::PermissionDenied))
        } else {
            tokio::task::yield_now().await;
            Ok(42)
        }
    })
    .await;
    assert_eq!(result.unwrap(), 42);
}

#[tokio::test]
async fn all_failed_addresses_preserve_terminal_errors_in_either_order() {
    for terminal_port in [1, 2] {
        let addresses = vec!["[::1]:1".parse().unwrap(), "127.0.0.1:2".parse().unwrap()];
        let result = connect_addresses(addresses, |addr| async move {
            if addr.port() == 2 {
                tokio::task::yield_now().await;
            }
            Err::<(), _>(io::Error::from(if addr.port() == terminal_port {
                io::ErrorKind::PermissionDenied
            } else {
                io::ErrorKind::ConnectionRefused
            }))
        })
        .await;
        assert_eq!(result, Err(Code::PermissionDenied));
    }
}

#[tokio::test]
async fn dns_localhost_and_refusal() {
    let addresses = resolve("localhost", 1).await.unwrap();
    assert!(!addresses.is_empty());
    assert!(addresses.iter().all(|addr| addr.ip().is_loopback()));
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    drop(listener);
    assert_eq!(
        connect_addresses(vec![addr], TcpStream::connect)
            .await
            .unwrap_err(),
        Code::ConnectionRefused
    );
}

#[test]
fn typed_errors_distinguish_retryable_readiness_from_terminal_faults() {
    for kind in [
        io::ErrorKind::ConnectionReset,
        io::ErrorKind::NetworkUnreachable,
        io::ErrorKind::HostUnreachable,
    ] {
        assert!(network_io(kind).retryable());
    }
    assert_eq!(
        network_io(io::ErrorKind::PermissionDenied),
        Code::PermissionDenied
    );
    assert!(!network_io(io::ErrorKind::OutOfMemory).retryable());
    let cert = rustls::Error::InvalidCertificate(rustls::CertificateError::UnknownIssuer);
    let nested = io::Error::other(io::Error::other(cert));
    assert_eq!(error_chain_code(&nested), Some(Code::TlsCertificate));
    assert_eq!(
        file_metadata(Err(io::Error::from(io::ErrorKind::PermissionDenied))),
        Err(Code::PermissionDenied)
    );
    assert_eq!(
        file_metadata(Err(io::Error::from(io::ErrorKind::NotFound))),
        Err(Code::FileMissing)
    );
    assert_eq!(
        file_metadata(Err(io::Error::from(io::ErrorKind::NotADirectory))),
        Err(Code::Filesystem)
    );
    assert!(matches!(
        native_tls(rustls_native_certs::CertificateResult::default()),
        Err(Code::TrustStore)
    ));
    let dir = tempfile::tempdir().unwrap();
    let loaded = rustls_native_certs::load_certs_from_paths(Some(&dir.path().join("absent")), None);
    assert!(!loaded.errors.is_empty());
    assert!(matches!(native_tls(loaded), Err(Code::TrustStore)));
    let mut malformed = rustls_native_certs::CertificateResult::default();
    malformed.certs.push(vec![0].into());
    assert!(matches!(native_tls(malformed), Err(Code::TrustStore)));
}

async fn tls_server(
    name: &str,
) -> (
    Url,
    rustls::pki_types::CertificateDer<'static>,
    tokio::task::JoinHandle<()>,
) {
    let key = rcgen::KeyPair::generate().unwrap();
    let cert = rcgen::CertificateParams::new(vec![name.to_owned()])
        .unwrap()
        .self_signed(&key)
        .unwrap();
    let der = cert.der().clone();
    let server = rustls::ServerConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .unwrap()
    .with_no_client_auth()
    .with_single_cert(
        vec![der.clone()],
        rustls::pki_types::PrivatePkcs8KeyDer::from(key.serialize_der()).into(),
    )
    .unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = Url::parse(&format!(
        "https://127.0.0.1:{}/secret-marker",
        listener.local_addr().unwrap().port()
    ))
    .unwrap();
    let handle = tokio::spawn(async move {
        let (stream, _) = listener.accept().await.unwrap();
        if let Ok(mut stream) = tokio_rustls::TlsAcceptor::from(Arc::new(server))
            .accept(stream)
            .await
        {
            let mut buffer = [0; 2048];
            let _ = stream.read(&mut buffer).await;
            let _ = stream
                .write_all(b"HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
                .await;
        }
    });
    (url, der, handle)
}

#[tokio::test]
async fn tls_trust_hostname_and_untrusted_certificates() {
    for (name, trust, expected) in [
        ("127.0.0.1", true, Ok(())),
        ("127.0.0.1", false, Err(Code::TlsCertificate)),
        ("different.example", true, Err(Code::TlsCertificate)),
    ] {
        let (url, cert, server) = tls_server(name).await;
        let mut roots = rustls::RootCertStore::empty();
        if trust {
            roots.add(cert).unwrap();
        }
        let client = http_client(Some(tls_with_roots(roots).unwrap())).unwrap();
        assert_eq!(
            timeout(
                Duration::from_secs(3),
                http(&client, &url, Method::Get, None)
            )
            .await
            .unwrap(),
            expected
        );
        server.await.unwrap();
    }
}

#[tokio::test]
async fn tls_retains_usable_native_roots_when_other_entries_fail() {
    for (loader_error, malformed_certificate) in [(true, false), (false, true), (true, true)] {
        let (url, cert, server) = tls_server("127.0.0.1").await;
        let dir = tempfile::tempdir().unwrap();
        let mut loaded = if loader_error {
            let loaded =
                rustls_native_certs::load_certs_from_paths(Some(&dir.path().join("absent")), None);
            assert!(!loaded.errors.is_empty());
            loaded
        } else {
            rustls_native_certs::CertificateResult::default()
        };
        if malformed_certificate {
            loaded.certs.push(vec![0].into());
        }
        loaded.certs.push(cert);
        let client = http_client(Some(native_tls(loaded).unwrap())).unwrap();
        assert_eq!(
            timeout(
                Duration::from_secs(3),
                http(&client, &url, Method::Get, None)
            )
            .await
            .unwrap(),
            Ok(())
        );
        server.await.unwrap();
    }
}

#[tokio::test]
async fn headers_complete_without_waiting_for_body_and_head_is_literal() {
    for method in [Method::Get, Method::Head] {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = Url::parse(&format!("http://{}/", listener.local_addr().unwrap())).unwrap();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut buffer = [0; 2048];
            let n = stream.read(&mut buffer).await.unwrap();
            let request = std::str::from_utf8(&buffer[..n]).unwrap();
            assert!(request.starts_with(match method {
                Method::Get => "GET /",
                Method::Head => "HEAD /",
            }));
            assert!(!request.to_lowercase().contains("authorization"));
            stream
                .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 999999\r\n\r\n")
                .await
                .unwrap();
            let mut buffer = [0; 1];
            // Dropping the response/client closes the connection without body reads.
            assert_eq!(
                timeout(Duration::from_secs(2), stream.read(&mut buffer))
                    .await
                    .unwrap()
                    .unwrap(),
                0
            );
        });
        let client = http_client(None).unwrap();
        timeout(Duration::from_secs(1), http(&client, &url, method, None))
            .await
            .unwrap()
            .unwrap();
        drop(client);
        server.await.unwrap();
    }
}
