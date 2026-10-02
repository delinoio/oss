// SPDX-License-Identifier: Apache-2.0
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <string.h>
#include <sys/event.h>
#include <unistd.h>

#define CHECK(expression) do { if (!(expression)) { \
    fprintf(stderr, "fcntl conformance line=%d errno=%d\n", __LINE__, errno); \
    return 1; \
} } while (0)

static int execute(void) {
#ifdef SETFD
    int fd = kqueue();
    CHECK(fd >= 0);
    CHECK(fcntl(fd, F_SETFD, FD_CLOEXEC) == 0);
    CHECK(fcntl(fd, F_GETFD) == FD_CLOEXEC);
    CHECK(close(fd) == 0);
    puts("fcntl-setfd-ok");
#else
    int fd = open(".", O_RDONLY);
    CHECK(fd >= 0);
    char actual[PATH_MAX] = {0}, expected[PATH_MAX] = {0};
    CHECK(getcwd(expected, sizeof(expected)));
    CHECK(fcntl(fd, F_GETPATH, actual) == 0);
    CHECK(!strcmp(actual, expected));
    int duplicated = fcntl(fd, F_DUPFD, 64);
    CHECK(duplicated >= 64);
    int cloexec = fcntl(fd, F_DUPFD_CLOEXEC, 96);
    CHECK(cloexec >= 96);
    CHECK(fcntl(cloexec, F_GETFD) == FD_CLOEXEC);
    errno = 0;
    CHECK(fcntl(-1, F_GETFD) == -1 && errno == EBADF);
    errno = 0;
    CHECK(fcntl(-1, F_SETFD, FD_CLOEXEC) == -1 && errno == EBADF);
    memset(actual, 0x5a, sizeof(actual));
    errno = 0;
    CHECK(fcntl(-1, F_GETPATH, actual) == -1 && errno == EBADF);
    for (size_t i = 0; i < sizeof(actual); i++) CHECK(actual[i] == 0x5a);
    CHECK(close(duplicated) == 0 && close(cloexec) == 0 && close(fd) == 0);
    puts("fcntl-abi-ok");
#endif
    fflush(stdout);
    return 0;
}

#ifdef CONSTRUCTOR
static int result = 99;
__attribute__((constructor)) static void setup(void) { result = execute(); }
int probe_result(void) { return result; }
#else
int main(void) { return execute(); }
#endif
