#!/bin/sh
# Keep the container alive long enough for the first bootstrap marker probe,
# then fail within the startup observation to reproduce the readiness race.
sleep 0.3
exit 23
