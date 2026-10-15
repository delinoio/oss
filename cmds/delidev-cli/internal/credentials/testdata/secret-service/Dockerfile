FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
RUN apt-get update && apt-get install -y --no-install-recommends dbus gnome-keyring \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --home-dir /home/delidev-secret-test delidev-secret-test \
    && chmod 700 /home/delidev-secret-test
COPY credentials.test /usr/local/bin/credentials.test
COPY run.sh /usr/local/bin/run.sh
USER delidev-secret-test
ENTRYPOINT ["dbus-run-session", "--", "/usr/local/bin/run.sh"]
