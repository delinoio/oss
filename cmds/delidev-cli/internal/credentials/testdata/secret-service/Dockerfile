FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
RUN apt-get update && apt-get install -y --no-install-recommends dbus gnome-keyring \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --home-dir /home/delidev-secret-test delidev-secret-test \
    && chmod 700 /home/delidev-secret-test
COPY credentials.test /usr/local/bin/credentials.test
COPY run.sh /usr/local/bin/run.sh
USER delidev-secret-test
ENTRYPOINT ["dbus-run-session", "--", "/usr/local/bin/run.sh"]
