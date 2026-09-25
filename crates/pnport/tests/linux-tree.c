#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <unistd.h>

int main(int argc, char **argv) {
    pid_t child = fork();
    if (child < 0) return 31;
    if (child == 0) {
        if (setsid() < 0) return 32;
        for (;;) pause();
    }
    FILE *marker = fopen("detached.pid", "w");
    if (!marker) return 33;
    fprintf(marker, "%d\n", child);
    fclose(marker);
    if (argc > 1 && strcmp(argv[1], "failure") == 0) {
        char path[4096];
        if (snprintf(path, sizeof(path), "%s/failure", getenv("PNPORT_SESSION")) <= 0) return 34;
        FILE *failure = fopen(path, "w");
        if (!failure) return 35;
        fputs("PNPORT_INJECTION_FAILED", failure);
        fclose(failure);
    }
    for (;;) pause();
}
