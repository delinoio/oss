FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends dbus gnome-keyring \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --home-dir /home/delidev-secret-test delidev-secret-test \
    && chmod 700 /home/delidev-secret-test
COPY credentials.test /usr/local/bin/credentials.test
COPY run.sh /usr/local/bin/run.sh
USER delidev-secret-test
ENTRYPOINT ["dbus-run-session", "--", "/usr/local/bin/run.sh"]
