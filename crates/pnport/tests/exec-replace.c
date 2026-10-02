// SPDX-License-Identifier: Apache-2.0
// Vector and variadic libc replacement paths with literal arguments and no
// caller-provided injection environment, including nested ZIP-backed images.
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>
#ifdef __APPLE__
#include <crt_externs.h>
#else
extern char **environ;
#endif

#define PAD8 "pad", "pad", "pad", "pad", "pad", "pad", "pad", "pad"
#define ARGUMENTS(role, mode) "literal argv zero", (char *)role, (char *)mode, "", "two words", \
    "Unicode \xe2\x98\x83", "literal;$()'\"", PAD8, PAD8, PAD8, PAD8, PAD8

static int dependency(void) {
    char bytes[32] = {0};
    FILE *file = fopen("node_modules/dep/file.txt", "r");
    if (!file) return 40;
    size_t count = fread(bytes, 1, sizeof(bytes), file);
    fclose(file);
    return count == 13 && memcmp(bytes, "package bytes", 13) == 0 ? 0 : 41;
}

static int replace(const char *mode, const char *path, const char *role) {
    char *arguments[] = {ARGUMENTS(role, mode), NULL};
    char *path_environment = strcmp(mode, "execvp-privileged") == 0
        ? "PATH=privileged:node_modules/dep/bin"
        : strcmp(mode, "execvp") == 0 || strcmp(mode, "execlp") == 0
            ? "PATH=node_modules/dep/missing:node_modules/dep/blocked:node_modules/dep/bin"
            : "PATH=/absent-child-path";
    char *replacement[] = {"PNPORT_TEST_ENV=replacement", path_environment, NULL};
    char *parent[] = {"PNPORT_TEST_ENV=parent", "PATH=/absent-parent-path", NULL};
#ifdef __APPLE__
    *_NSGetEnviron() = strcmp(mode, "execle") == 0 ? parent : replacement;
#else
    environ = strcmp(mode, "execle") == 0 ? parent : replacement;
#endif
    if (strcmp(mode, "execv") == 0) return execv(path, arguments);
    if (strcmp(mode, "execve") == 0) return execve(path, arguments, replacement);
    if (strcmp(mode, "execvp") == 0 || strcmp(mode, "execvp-privileged") == 0)
        return execvp(path, arguments);
    if (strcmp(mode, "execl") == 0)
        return execl(path, ARGUMENTS(role, mode), (char *)NULL);
    if (strcmp(mode, "execle") == 0)
        return execle(path, ARGUMENTS(role, mode), (char *)NULL, replacement);
    if (strcmp(mode, "execlp") == 0)
        return execlp(path, ARGUMENTS(role, mode), (char *)NULL);
#ifdef __APPLE__
    if (strcmp(mode, "execvP") == 0)
        return execvP(path, "node_modules/dep/missing:node_modules/dep/blocked:node_modules/dep/bin", arguments);
#endif
    return -1;
}

int main(int argc, char **argv) {
    const char *role, *mode;
    if (argc == 3 && strcmp(argv[1], "root") == 0) {
        role = argv[1]; mode = argv[2];
        FILE *group = fopen("root.group", "w");
        if (!group) return 42;
        fprintf(group, "%d", getpgrp());
        fclose(group);
    } else {
        if (argc != 47 || strcmp(argv[0], "literal argv zero") ||
            strcmp(argv[3], "") || strcmp(argv[4], "two words") ||
            strcmp(argv[5], "Unicode \xe2\x98\x83") || strcmp(argv[6], "literal;$()'\""))
            return 43;
        for (int index = 7; index < argc; index++)
            if (strcmp(argv[index], "pad")) return 44;
        role = argv[1]; mode = argv[2];
        FILE *accepted = fopen("exec.accepted", "w");
        if (!accepted) return 54;
        fputs("1", accepted);
        fclose(accepted);
        const char *value = getenv("PNPORT_TEST_ENV");
        if (!value || strcmp(value, "replacement")) return 45;
        value = getenv("PATH");
        const char *expected = strcmp(mode, "execvp") == 0 || strcmp(mode, "execlp") == 0
            ? "node_modules/dep/missing:node_modules/dep/blocked:node_modules/dep/bin"
            : "/absent-child-path";
        if (!value || strcmp(value, expected)) return 46;
    }
    int result = dependency();
    if (result) return result;
    if (strcmp(mode, "protected") == 0) {
        replace("execle", "/usr/bin/true", "leaf");
        return 55;
    }
    if (strcmp(mode, "privileged") == 0) {
        replace("execvp-privileged", "tree", "leaf");
        return 55;
    }
    if (strcmp(mode, "shell-fallback") == 0) {
        replace("execvp", "./bad-format", "leaf");
        return 55;
    }
    if (strcmp(role, "leaf") == 0) return 23;
    pid_t child = fork();
    if (child < 0) return 48;
    if (!child) {
        const char *next = strcmp(role, "root") == 0 ? "middle" : "leaf";
        errno = 0;
        if (replace(mode, "node_modules/dep/bin/absent", next) != -1 || errno != ENOENT)
            _exit(49);
        errno = 0;
        if (replace(mode, "node_modules/dep/bin/noexec", next) != -1 || errno != EACCES) {
            fprintf(stderr, "exec_permission_probe_failed: errno=%d\n", errno);
            _exit(52);
        }
        errno = 0;
        if (replace(mode, "./loop", next) != -1 || errno != ELOOP) {
            fprintf(stderr, "exec_loop_probe_failed: errno=%d\n", errno);
            _exit(53);
        }
        if (strcmp(mode, "execve") == 0 || strcmp(mode, "execv") == 0 ||
            strcmp(mode, "execl") == 0 || strcmp(mode, "execle") == 0) {
            errno = 0;
            if (replace(mode, "./bad-format", next) != -1 || errno != ENOEXEC) {
                fprintf(stderr, "exec_format_probe_failed: errno=%d\n", errno);
                _exit(56);
            }
        }
        const char *path = strcmp(mode, "execvp") == 0 || strcmp(mode, "execlp") == 0 || strcmp(mode, "execvP") == 0
            ? "tree" : "node_modules/dep/bin/tree";
        replace(mode, path, next);
        _exit(50);
    }
    int status;
    if (waitpid(child, &status, 0) != child || !WIFEXITED(status)) return 51;
    return WEXITSTATUS(status);
}
