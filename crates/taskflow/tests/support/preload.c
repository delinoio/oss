/* A loader constructor executes before main. Its detached double-fork child
 * must still belong to the task's kernel owner, and it must never run in the
 * bootstrap helper itself. The marker contains PIDs only. */
#define _DEFAULT_SOURCE
#include <assert.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

extern char **environ;
__attribute__((constructor)) static void preload(void) {
    const char *marker = getenv("TFLOW_PRELOAD_MARKER");
    assert(marker);
    int fd = open(marker, O_WRONLY | O_CREAT | O_APPEND, 0600);
    assert(fd >= 0);
    char record[64];
    int size = snprintf(record, sizeof(record), "%d\n", getpid());
    assert(write(fd, record, (size_t)size) == size);
    close(fd);
    char child_path[4096], temporary[4096];
    assert(snprintf(child_path, sizeof(child_path), "%s.child", marker) < (int)sizeof(child_path));
    assert(snprintf(temporary, sizeof(temporary), "%s.pending", marker) < (int)sizeof(temporary));
    pid_t middle = fork();
    assert(middle >= 0);
    if (!middle) {
        assert(setsid() > 0);
        pid_t leaf = fork();
        assert(leaf >= 0);
        if (leaf) _exit(0);
        assert(setsid() > 0);
        environ = NULL;
        assert(chdir("/") == 0);
        int null = open("/dev/null", O_RDWR);
        assert(null >= 0);
        for (int i = 0; i < 3; i++) assert(dup2(null, i) >= 0);
        if (null > 2) close(null);
        fd = open(temporary, O_WRONLY | O_CREAT | O_EXCL, 0600);
        assert(fd >= 0);
        size = snprintf(record, sizeof(record), "%d\n", getpid());
        assert(write(fd, record, (size_t)size) == size);
        close(fd);
        assert(rename(temporary, child_path) == 0);
        for (;;) sleep(60);
    }
    int status;
    assert(waitpid(middle, &status, 0) == middle && WIFEXITED(status) && !WEXITSTATUS(status));
    while (access(child_path, F_OK)) usleep(1000);
}
