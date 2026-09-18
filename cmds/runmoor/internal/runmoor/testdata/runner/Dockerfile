ARG RUNNER_BASE
FROM ${RUNNER_BASE}
USER root
COPY run.sh /home/runner/run.sh
COPY failing-run.sh /home/runner/failing/run.sh
RUN chmod 755 /home/runner/run.sh /home/runner/failing/run.sh && ln -s /home/runner/bin /home/runner/failing/bin
USER runner
