use std::{
    error::Error,
    future::Future,
    io,
    net::{IpAddr, SocketAddr},
    path::PathBuf,
    sync::{Arc, OnceLock},
};

use futures_util::{
    future::{BoxFuture, Shared},
    stream::FuturesUnordered,
    FutureExt, StreamExt,
};
use hickory_resolver::{config::LookupIpStrategy, TokioResolver};
use reqwest::{
    dns::{Addrs, Name, Resolve, Resolving},
    Client, Url,
};
use tokio::net::TcpStream;

use crate::{
    cli::{Method, TcpTarget},
    wait::Code,
};

pub enum Target {
    Tcp(TcpTarget),
    Http {
        url: Url,
        method: Method,
        status: Option<u16>,
        client: HttpClient,
    },
    File(PathBuf),
}

impl Target {
    pub async fn check(&self) -> Result<(), Code> {
        match self {
            Self::Tcp(target) => {
                let addresses = resolve(&target.host, target.port).await?;
                // All resolved addresses get an opportunity in this one attempt
                // budget. A black-holed first family cannot starve another one.
                connect_addresses(addresses, TcpStream::connect)
                    .await
                    .map(drop)
            }
            Self::Http {
                url,
                method,
                status,
                client,
            } => {
                let client = client
                    .get_or_init({
                        let https = url.scheme() == "https";
                        move || {
                            let tls = if https {
                                Some(native_tls(rustls_native_certs::load_native_certs())?)
                            } else {
                                None
                            };
                            http_client(tls)
                        }
                    })
                    .await?;
                http(&client, url, *method, *status).await
            }
            Self::File(path) => file_metadata(tokio::fs::metadata(path).await),
        }
    }
}

type InitializedClient = Shared<BoxFuture<'static, Result<Client, Code>>>;

#[derive(Default)]
pub struct HttpClient {
    initialization: OnceLock<InitializedClient>,
}

impl HttpClient {
    async fn get_or_init<F>(&self, initialize: F) -> Result<Client, Code>
    where
        F: FnOnce() -> Result<Client, Code> + Send + 'static,
    {
        self.initialization
            .get_or_init(|| {
                // A blocking OS trust load cannot be cancelled. Retain its shared
                // result across dropped attempt futures so retries await the same
                // job instead of accumulating overlapping native loaders.
                tokio::task::spawn_blocking(initialize)
                    .map(|result| result.unwrap_or(Err(Code::TrustStore)))
                    .boxed()
                    .shared()
            })
            .clone()
            .await
    }
}

pub fn file_metadata(result: io::Result<std::fs::Metadata>) -> Result<(), Code> {
    match result {
        Ok(metadata) if metadata.is_file() => Ok(()),
        Ok(_) => Err(Code::NotRegularFile),
        Err(error) => Err(match error.kind() {
            io::ErrorKind::NotFound => Code::FileMissing,
            io::ErrorKind::PermissionDenied => Code::PermissionDenied,
            _ => Code::Filesystem,
        }),
    }
}

async fn resolve(host: &str, port: u16) -> Result<Vec<SocketAddr>, Code> {
    if let Ok(ip) = host.parse::<IpAddr>() {
        return Ok(vec![SocketAddr::new(ip, port)]);
    }
    let resolver = tokio::task::spawn_blocking(|| {
        let mut builder = TokioResolver::builder_tokio().map_err(|_| Code::DnsConfiguration)?;
        builder.options_mut().ip_strategy = LookupIpStrategy::Ipv4AndIpv6;
        builder.options_mut().cache_size = 0;
        builder.options_mut().attempts = 1;
        builder.build().map_err(|_| Code::DnsConfiguration)
    })
    .await
    .map_err(|_| Code::DnsConfiguration)??;
    let lookup = resolver
        .lookup_ip(host)
        .await
        .map_err(|error| error_chain_code(&error).unwrap_or(Code::DnsLookup))?;
    let addresses: Vec<_> = lookup.iter().map(|ip| SocketAddr::new(ip, port)).collect();
    if addresses.is_empty() {
        return Err(Code::DnsLookup);
    }
    Ok(addresses)
}

async fn connect_addresses<C, F, T>(addresses: Vec<SocketAddr>, connect: C) -> Result<T, Code>
where
    C: Fn(SocketAddr) -> F,
    F: Future<Output = io::Result<T>>,
{
    let mut pending: FuturesUnordered<_> = addresses.into_iter().map(connect).collect();
    let mut last = Code::DnsLookup;
    let mut terminal = None;
    while let Some(result) = pending.next().await {
        match result {
            Ok(stream) => return Ok(stream),
            Err(error) => {
                last = network_io(error.kind());
                if !last.retryable() {
                    // A local error may affect only this destination or family.
                    // Preserve it if all addresses fail, but let others connect.
                    terminal.get_or_insert(last);
                }
            }
        }
    }
    Err(terminal.unwrap_or(last))
}

struct Resolver;
impl Resolve for Resolver {
    fn resolve(&self, name: Name) -> Resolving {
        Box::pin(async move {
            let addresses = resolve(name.as_str(), 0)
                .await
                .map_err(|code| Box::new(code) as Box<dyn Error + Send + Sync>)?;
            Ok(Box::new(addresses.into_iter()) as Addrs)
        })
    }
}

fn native_tls(
    loaded: rustls_native_certs::CertificateResult,
) -> Result<rustls::ClientConfig, Code> {
    let mut roots = rustls::RootCertStore::empty();
    // OS stores can contain unreadable or unsupported entries alongside usable
    // roots. Keep usable roots without exposing loader errors or certificate data.
    let (usable_roots, ignored_certificates) = roots.add_parsable_certificates(loaded.certs);
    tracing::debug!(
        kind = "http",
        usable_roots,
        ignored_certificates,
        loader_errors = loaded.errors.len(),
        "native_trust_loaded"
    );
    if roots.is_empty() {
        return Err(Code::TrustStore);
    }
    tls_with_roots(roots)
}

fn tls_with_roots(roots: rustls::RootCertStore) -> Result<rustls::ClientConfig, Code> {
    let mut config = rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(|_| Code::TrustStore)?
    .with_root_certificates(roots)
    .with_no_client_auth();
    // Reqwest preserves ALPN on preconfigured Rustls clients, so advertise both
    // supported protocols here instead of relying on its default TLS builder.
    config.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];
    Ok(config)
}

fn http_client(tls: Option<rustls::ClientConfig>) -> Result<Client, Code> {
    let mut builder = Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .retry(reqwest::retry::never())
        .referer(false)
        .pool_max_idle_per_host(0)
        .dns_resolver(Arc::new(Resolver));
    // Always select a known Rustls provider, even when workspace feature
    // unification also enables reqwest's native-TLS or bundled-root features.
    builder = builder.use_preconfigured_tls(match tls {
        Some(tls) => tls,
        None => tls_with_roots(rustls::RootCertStore::empty())?,
    });
    builder.build().map_err(|_| Code::NetworkIo)
}

async fn http(client: &Client, url: &Url, method: Method, status: Option<u16>) -> Result<(), Code> {
    let method = match method {
        Method::Get => reqwest::Method::GET,
        Method::Head => reqwest::Method::HEAD,
    };
    // send() completes at response headers. Drop the response immediately;
    // never poll its body, including for an unexpected status or redirect.
    let response = client
        .request(method, url.clone())
        .send()
        .await
        .map_err(|error| {
            error_chain_code(&error).unwrap_or_else(|| {
                if error.is_timeout() {
                    Code::AttemptTimeout
                } else {
                    Code::HttpProtocol
                }
            })
        })?;
    let observed = response.status().as_u16();
    tracing::debug!(kind = "http", http_status = observed, "http_headers");
    if status
        .map(|expected| expected == observed)
        .unwrap_or((200..300).contains(&observed))
    {
        Ok(())
    } else {
        Err(Code::UnexpectedStatus)
    }
}

fn network_io(kind: io::ErrorKind) -> Code {
    match kind {
        io::ErrorKind::PermissionDenied => Code::PermissionDenied,
        io::ErrorKind::ConnectionRefused => Code::ConnectionRefused,
        io::ErrorKind::TimedOut => Code::AttemptTimeout,
        io::ErrorKind::ConnectionReset
        | io::ErrorKind::ConnectionAborted
        | io::ErrorKind::NotConnected
        | io::ErrorKind::AddrNotAvailable
        | io::ErrorKind::BrokenPipe
        | io::ErrorKind::WouldBlock
        | io::ErrorKind::Interrupted
        | io::ErrorKind::UnexpectedEof
        | io::ErrorKind::NetworkUnreachable
        | io::ErrorKind::HostUnreachable
        | io::ErrorKind::NetworkDown => Code::NetworkUnavailable,
        _ => Code::NetworkIo,
    }
}

fn error_chain_code(mut error: &(dyn Error + 'static)) -> Option<Code> {
    let mut io_code = None;
    loop {
        if let Some(code) = error.downcast_ref::<Code>() {
            return Some(*code);
        }
        if let Some(error) = error.downcast_ref::<hyper::Error>() {
            if error.is_incomplete_message() || error.is_closed() || error.is_canceled() {
                return Some(Code::NetworkUnavailable);
            }
        }
        if let Some(error) = error.downcast_ref::<rustls::Error>() {
            return Some(match error {
                rustls::Error::InvalidCertificate(_) | rustls::Error::NoCertificatesPresented => {
                    Code::TlsCertificate
                }
                _ => Code::TlsProtocol,
            });
        }
        if let Some(error) = error.downcast_ref::<io::Error>() {
            io_code = Some(network_io(error.kind()));
            // io::Error::source skips the embedded wrapper itself in some
            // versions. Inspect get_ref so typed Rustls certificate errors
            // cannot be mistaken for retryable network I/O.
            if let Some(inner) = error.get_ref() {
                if let Some(code) = error_chain_code(inner) {
                    return Some(code);
                }
            }
        }
        match error.source() {
            Some(source) => error = source,
            None => return io_code,
        }
    }
}

#[cfg(test)]
#[path = "probe_tests.rs"]
mod tests;
