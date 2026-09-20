/* Private, embedded process owner. No task code runs before kernel ownership
 * exists. Enumeration selects processes to signal; ONLY the kernel's empty
 * ownership domain authorizes completion. Never replace that proof with a PID
 * snapshot or process-group absence check. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>
#ifdef __APPLE__
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/socket.h>
#include <sys/syscall.h>
#include <sys/un.h>
#else
#include <sys/prctl.h>
#endif

extern char **environ;
static volatile sig_atomic_t stopping;
static mode_t command_mask;
#ifdef __APPLE__
enum owner_phase {
    OWNER_INITIAL = 0, SCOPE_IDENTITY = 10, SCOPE_CONNECT, SCOPE_PIPES,
    SCOPE_ENVELOPE, SCOPE_FORK, SCOPE_WAIT, PROXY_CREATE = 20,
    PROXY_BOOTSTRAP, PROXY_ACCEPT, PROXY_IDENTITY, PROXY_ENVELOPE, PROXY_WAIT,
    PIPE_READ = 120, PIPE_MARKER, PIPE_TRUNCATED, PIPE_MISSING,
    PIPE_TYPE, PIPE_COUNT
};
static enum owner_phase phase;
#endif
static void stop_signal(int sig) { (void)sig; stopping = 1; }
static void signals(void) {
    struct sigaction action = {0};
    action.sa_handler = stop_signal;
    sigemptyset(&action.sa_mask);
    sigaction(SIGTERM, &action, NULL);
    sigaction(SIGINT, &action, NULL);
    signal(SIGPIPE, SIG_IGN);
}
static int64_t milliseconds(void) {
    struct timespec t;
    if (clock_gettime(CLOCK_MONOTONIC, &t)) _exit(125);
    return (int64_t)t.tv_sec * 1000 + t.tv_nsec / 1000000;
}
static void pause_tick(void) {
    struct timespec t = {0, 10000000};
    nanosleep(&t, NULL);
}
static int ended(int fd) {
    struct pollfd p = {fd, POLLIN | POLLHUP | POLLERR, 0};
    if (poll(&p, 1, 0) > 0 && p.revents) {
        char c;
        /* Any control message or EOF revokes the invocation. */
        return read(fd, &c, 1) <= 1;
    }
    return 0;
}
static int write_all(int fd, const void *buffer, size_t n) {
    const char *p = buffer;
    while (n) {
        ssize_t done = write(fd, p, n);
        if (done < 0 && errno == EINTR) continue;
        if (done <= 0) return -1;
        p += done; n -= (size_t)done;
    }
    return 0;
}
static int code_for(int status) {
    return WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
}
static pid_t start_command(char **args, char **env, const char *cwd, int out, int err) {
    pid_t pid = fork();
    if (pid != 0) return pid;
    signal(SIGTERM, SIG_DFL); signal(SIGINT, SIG_DFL); signal(SIGPIPE, SIG_DFL);
    if (setpgid(0, 0)) _exit(126);
    int input = open("/dev/null", O_RDONLY);
    if (input < 0 || dup2(input, 0) < 0 || dup2(out, 1) < 0 || dup2(err, 2) < 0) _exit(126);
    /* All non-stdio owner FDs are close-on-exec. The actual command never holds
     * the controller's EOF lease, even if it changes cwd/env or daemonizes. */
    if (input > 2) close(input);
    if (chdir(cwd)) _exit(126);
    umask(command_mask);
    environ = env;
    execvp(args[0], args);
    _exit(errno == ENOENT ? 127 : 126);
}
static int acknowledge(const char *scope, int code) {
    char path[512];
    if (snprintf(path, sizeof(path), "%s/complete", scope) >= (int)sizeof(path)) return 125;
    int fd = open(path, O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
    if (fd < 0) return 125;
    char message[64];
    int n = snprintf(message, sizeof(message), "TFLOW_OWNER_V1 %d\n", code);
    int failed = write_all(fd, message, (size_t)n);
    if (close(fd)) failed = -1;
    return failed ? 125 : code;
}

#ifndef __APPLE__
/* The helper, not the embedding Rust process, is a subreaper. Escaped sessions
 * and double forks are adopted here. A child cannot disappear from this domain
 * by changing its parent/group before enumeration: after each parent is reaped
 * its surviving children become direct children. ECHILD is the final proof. */
static int signal_children(int sig) {
    char path[128];
    snprintf(path, sizeof(path), "/proc/self/task/%d/children", getpid());
    FILE *f = fopen(path, "re");
    if (!f) return -1;
    int pid;
    while (fscanf(f, "%d", &pid) == 1) {
        /* An unreaped direct child's PID cannot be reused. Only this helper
         * waits for children, and it does not reap during this iteration. */
        if (kill(pid, sig) && errno != ESRCH) { fclose(f); return -1; }
    }
    int failed = ferror(f);
    fclose(f);
    return failed ? -1 : 0;
}
static int run_linux(const char *scope, char **args) {
    if (prctl(PR_SET_CHILD_SUBREAPER, 1) || fcntl(0, F_SETFD, FD_CLOEXEC)) return 125;
    char *cwd = getcwd(NULL, 0);
    if (!cwd) return 125;
    if (stopping || ended(0)) { free(cwd); return acknowledge(scope, 130); }
    pid_t root = start_command(args, environ, cwd, 1, 2);
    free(cwd);
    if (root < 0) return 125;
    int status = 0, root_done = 0, cleanup = 0, term_sent = 0, code = 125;
    int64_t force_at = 0;
    for (;;) {
        pid_t p;
        while ((p = waitpid(-1, &status, WNOHANG)) > 0) {
            if (p == root) { root_done = 1; code = code_for(status); }
        }
        if (p < 0 && errno == ECHILD) {
            if (!root_done) return 125;
            return acknowledge(scope, code);
        }
        if (p < 0 && errno != EINTR) return 125;
        if (!cleanup && (root_done || stopping || ended(0))) {
            cleanup = 1;
            force_at = milliseconds() + (root_done ? 0 : 2000);
        }
        if (cleanup && (milliseconds() >= force_at || !term_sent)) {
            if (signal_children(milliseconds() >= force_at ? SIGKILL : SIGTERM)) return 125;
            term_sent = 1;
        }
        pause_tick();
    }
}
#else
/* macOS 13 XNU 8792.41.9: resource-coalition membership is inherited by fork
 * and exec, independently of process groups and ancestry. A temporary launchd
 * job gives this invocation a distinct coalition before its start barrier.
 * The versioned kernel structures are checked and unsupported shapes fail
 * closed; bootout alone is never a descendant-cleanup proof.
 * https://github.com/apple-oss-distributions/xnu/blob/xnu-8792.41.9/bsd/kern/proc_info.c
 * https://github.com/apple-oss-distributions/xnu/blob/xnu-8792.41.9/osfmk/mach/coalition.h */
/* No public libproc wrapper exposes resource-coalition accounting. Limit this
 * compatibility exception to the two verified XNU calls; remove it if Apple
 * provides a supported wrapper with the same ownership/empty-domain contract. */
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static int coalition_for(pid_t pid, uint64_t *id) {
    uint64_t ids[5] = {0};
    int n = (int)syscall(SYS_proc_info, 2, pid, 20, 0, ids, sizeof(ids));
    if (n != (int)sizeof(ids) || !ids[0]) return -1;
    *id = ids[0]; return 0;
}
static int coalition_count(uint64_t id, uint64_t *count) {
    uint64_t usage[2] = {0}, size = sizeof(usage);
    if (syscall(SYS_coalition_info, 1, &id, usage, &size, 0, 0)) {
        if (errno == ESRCH) { *count = 0; return 0; }
        return -1;
    }
    if (usage[1] > usage[0]) return -1;
    *count = usage[0] - usage[1]; return 0;
}
#pragma clang diagnostic pop
static int signal_coalition(uint64_t id, pid_t exclude, int sig) {
    int bytes = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
    if (bytes <= 0) return -1;
    /* Growth only requires another iteration, never an empty-domain claim. */
    pid_t *pids = calloc(1, (size_t)bytes + 4096);
    if (!pids) return -1;
    int n = proc_listpids(PROC_ALL_PIDS, 0, pids, bytes + 4096);
    if (n <= 0) { free(pids); return -1; }
    for (size_t i = 0; i < (size_t)n / sizeof(pid_t); i++) {
        pid_t pid = pids[i];
        if (pid <= 0 || pid == exclude) continue;
        struct proc_bsdinfo before, after;
        uint64_t member;
        if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &before, sizeof(before)) != (int)sizeof(before)) continue;
        if (coalition_for(pid, &member) || member != id) continue;
        if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &after, sizeof(after)) != (int)sizeof(after)) continue;
        if (before.pbi_start_tvsec != after.pbi_start_tvsec || before.pbi_start_tvusec != after.pbi_start_tvusec) continue;
        if (coalition_for(pid, &member) || member != id) continue;
        if (kill(pid, sig) && errno != ESRCH) { free(pids); return -1; }
    }
    free(pids); return 0;
}
static int read_all(int fd, void *buffer, size_t n) {
    char *p = buffer;
    while (n) {
        ssize_t done = read(fd, p, n);
        if (done < 0 && errno == EINTR) continue;
        if (done <= 0) return -1;
        p += done; n -= (size_t)done;
    }
    return 0;
}
static int send_string(int fd, const char *s) {
    size_t n = strlen(s);
    if (n > 16 * 1024 * 1024) return -1;
    uint32_t size = (uint32_t)n;
    return write_all(fd, &size, sizeof(size)) || write_all(fd, s, size) ? -1 : 0;
}
static char *receive_string(int fd, size_t *budget) {
    uint32_t n;
    if (read_all(fd, &n, sizeof(n)) || n > *budget) return NULL;
    *budget -= n;
    char *s = malloc((size_t)n + 1);
    if (!s) return NULL;
    if (read_all(fd, s, n) || memchr(s, 0, n)) { free(s); return NULL; }
    s[n] = 0; return s;
}
static int send_vector(int fd, char **values) {
    uint32_t n = 0;
    while (values[n]) { if (++n > 65536) return -1; }
    if (write_all(fd, &n, sizeof(n))) return -1;
    for (uint32_t i = 0; i < n; i++) if (send_string(fd, values[i])) return -1;
    return 0;
}
static char **receive_vector(int fd, size_t *budget) {
    uint32_t n;
    if (read_all(fd, &n, sizeof(n)) || n > 65536) return NULL;
    char **v = calloc((size_t)n + 1, sizeof(char *));
    if (!v) return NULL;
    for (uint32_t i = 0; i < n; i++) {
        v[i] = receive_string(fd, budget);
        if (!v[i]) { for (uint32_t j = 0; j < i; j++) free(v[j]); free(v); return NULL; }
    }
    return v;
}
static void free_vector(char **v) { if (v) { for (size_t i = 0; v[i]; i++) free(v[i]); free(v); } }
static int stream_socket(void) {
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return -1;
    struct timeval timeout = {10, 0};
    if (fcntl(fd, F_SETFD, FD_CLOEXEC) || setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout)) || setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof(timeout))) { close(fd); return -1; }
    return fd;
}
static int socket_address(struct sockaddr_un *address, const char *scope) {
    memset(address, 0, sizeof(*address)); address->sun_family = AF_UNIX;
    return snprintf(address->sun_path, sizeof(address->sun_path), "%s/control", scope) >= (int)sizeof(address->sun_path) ? -1 : 0;
}
static int transfer_pipes(int fd, int *pipes, int sending) {
    char byte = 'P';
    struct iovec iov = {&byte, 1};
    union { struct cmsghdr alignment; char bytes[CMSG_SPACE(2 * sizeof(int))]; } control;
    memset(&control, 0, sizeof(control));
    struct msghdr message = {0};
    message.msg_iov = &iov; message.msg_iovlen = 1;
    message.msg_control = control.bytes; message.msg_controllen = sizeof(control.bytes);
    if (sending) {
        struct cmsghdr *c = CMSG_FIRSTHDR(&message);
        c->cmsg_level = SOL_SOCKET; c->cmsg_type = SCM_RIGHTS; c->cmsg_len = CMSG_LEN(2 * sizeof(int));
        memcpy(CMSG_DATA(c), pipes, 2 * sizeof(int));
        return sendmsg(fd, &message, 0) == 1 ? 0 : -1;
    }
    if (recvmsg(fd, &message, 0) != 1) { phase = PIPE_READ; return -1; }
    if (byte != 'P') { phase = PIPE_MARKER; return -1; }
    if (message.msg_flags & (MSG_CTRUNC | MSG_TRUNC)) { phase = PIPE_TRUNCATED; return -1; }
    struct cmsghdr *c = CMSG_FIRSTHDR(&message);
    if (!c) { phase = PIPE_MISSING; return -1; }
    if (c->cmsg_level != SOL_SOCKET || c->cmsg_type != SCM_RIGHTS) { phase = PIPE_TYPE; return -1; }
    if (c->cmsg_len != CMSG_LEN(2 * sizeof(int))) { phase = PIPE_COUNT; return -1; }
    memcpy(pipes, CMSG_DATA(c), 2 * sizeof(int));
    return fcntl(pipes[0], F_SETFD, FD_CLOEXEC) || fcntl(pipes[1], F_SETFD, FD_CLOEXEC) ? -1 : 0;
}
static int launchctl(const char *operation, const char *target, const char *file) {
    pid_t child = fork();
    if (child < 0) return -1;
    if (!child) {
        signal(SIGTERM, SIG_DFL); signal(SIGINT, SIG_DFL);
        int null = open("/dev/null", O_RDWR);
        if (null < 0) _exit(125);
        dup2(null, 0); dup2(null, 1); dup2(null, 2);
        char *env[] = {"PATH=/usr/bin:/bin", "LC_ALL=C", NULL};
        char *args[] = {"/bin/launchctl", (char *)operation, (char *)target, (char *)file, NULL};
        execve(args[0], args, env); _exit(125);
    }
    int status;
    int64_t deadline = milliseconds() + 10000;
    for (;;) {
        pid_t p = waitpid(child, &status, WNOHANG);
        if (p == child) return WIFEXITED(status) && WEXITSTATUS(status) == 0 ? 0 : -1;
        if (p < 0 && errno != EINTR) return -1;
        if (milliseconds() >= deadline) {
            kill(child, SIGKILL); while (waitpid(child, &status, 0) < 0 && errno == EINTR) {}
            return -1;
        }
        pause_tick();
    }
}
static void xml_string(FILE *f, const char *value) {
    fputs("<string>", f);
    for (const char *c = value; *c; c++) {
        switch (*c) {
        case '&': fputs("&amp;", f); break;
        case '<': fputs("&lt;", f); break;
        case '>': fputs("&gt;", f); break;
        case '\"': fputs("&quot;", f); break;
        case '\'': fputs("&apos;", f); break;
        default: fputc(*c, f);
        }
    }
    fputs("</string>", f);
}
static int supervisor_mac(const char *scope) {
    phase = SCOPE_IDENTITY;
    uint64_t id, count;
    if (coalition_for(getpid(), &id) || coalition_count(id, &count) || count != 1) return 125;
    phase = SCOPE_CONNECT;
    int fd = stream_socket();
    struct sockaddr_un address;
    if (fd < 0 || socket_address(&address, scope) || connect(fd, (struct sockaddr *)&address, sizeof(address))) return 125;
    uint64_t hello[2] = {(uint64_t)getpid(), id};
    if (write_all(fd, hello, sizeof(hello))) return 125;
    phase = SCOPE_PIPES;
    int pipes[2];
    if (transfer_pipes(fd, pipes, 0)) return 125;
    phase = SCOPE_ENVELOPE;
    size_t budget = 64 * 1024 * 1024;
    char *cwd = receive_string(fd, &budget);
    uint32_t mask;
    if (read_all(fd, &mask, sizeof(mask)) || mask > 0777) return 125;
    command_mask = (mode_t)mask;
    char **args = receive_vector(fd, &budget), **env = receive_vector(fd, &budget);
    if (!cwd || !args || !args[0] || !env) return 125;
    /* The sender can disappear while its envelope is being received. A closed
     * lease must never release the start barrier. */
    if (stopping || ended(fd) || coalition_count(id, &count) || count != 1) return 125;
    phase = SCOPE_FORK;
    pid_t root = start_command(args, env, cwd, pipes[0], pipes[1]);
    close(pipes[0]); close(pipes[1]); free(cwd); free_vector(args); free_vector(env);
    if (root < 0) return 125;
    phase = SCOPE_WAIT;
    int root_done = 0, code = 125, cleanup = 0, term_sent = 0, status;
    int64_t force_at = 0;
    for (;;) {
        if (!root_done) {
            pid_t p = waitpid(root, &status, WNOHANG);
            if (p == root) { root_done = 1; code = code_for(status); }
            else if (p < 0 && errno != EINTR) return 125;
        }
        if (!cleanup && (root_done || stopping || ended(fd))) {
            cleanup = 1; force_at = milliseconds() + (root_done ? 0 : 2000);
        }
        if (cleanup) {
            if (coalition_count(id, &count) || count == 0) return 125;
            if (count == 1 && root_done) {
                (void)write_all(fd, &code, sizeof(code));
                close(fd);
                /* The proxy may have died. Self-removal cleans the launchd job
                 * even when no controller is available to run bootout. */
                char target[256];
                snprintf(target, sizeof(target), "user/%d/io.delino.tflow.%s", getuid(), strrchr(scope, '/') + 1);
                char *a[] = {"/bin/launchctl", "bootout", target, NULL};
                char *e[] = {"PATH=/usr/bin:/bin", "LC_ALL=C", NULL};
                execve(a[0], a, e); return 125;
            }
            if (milliseconds() >= force_at || !term_sent) {
                if (signal_coalition(id, getpid(), milliseconds() >= force_at ? SIGKILL : SIGTERM)) return 125;
                term_sent = 1;
            }
        }
        pause_tick();
    }
}
static int run_mac(const char *scope, const char *executable, char **args) {
    phase = PROXY_CREATE;
    char plist[512], label[256], domain[64], target[320];
    snprintf(label, sizeof(label), "io.delino.tflow.%s", strrchr(scope, '/') + 1);
    snprintf(domain, sizeof(domain), "user/%d", getuid());
    snprintf(target, sizeof(target), "%s/%s", domain, label);
    snprintf(plist, sizeof(plist), "%s/supervisor.plist", scope);
    int listener = stream_socket();
    struct sockaddr_un address;
    if (listener < 0 || socket_address(&address, scope) || bind(listener, (struct sockaddr *)&address, sizeof(address)) || listen(listener, 1)) return 125;
    FILE *f = fopen(plist, "wx");
    if (!f) return 125;
    fputs("<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>Label</key>", f); xml_string(f, label);
    fputs("<key>ProgramArguments</key><array>", f);
    xml_string(f, executable); xml_string(f, "supervise"); xml_string(f, scope);
    fputs("</array><key>LimitLoadToSessionType</key><string>Background</string><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>ExitTimeOut</key><integer>1</integer><key>AbandonProcessGroup</key><true/><key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/bin:/bin</string></dict></dict></plist>", f);
    int failed = fclose(f);
    uint64_t hello[2] = {0}, count = 0, actual = 0, owned_id = 0;
    int control = -1, code = 125, complete = 0;
    phase = PROXY_BOOTSTRAP;
    if (failed || launchctl("bootstrap", domain, plist)) goto cleanup;
    phase = PROXY_ACCEPT;
    struct pollfd ready = {listener, POLLIN, 0};
    if (poll(&ready, 1, 10000) <= 0) goto cleanup;
    control = accept(listener, NULL, NULL);
    if (control < 0 || fcntl(control, F_SETFD, FD_CLOEXEC)) goto cleanup;
    struct timeval timeout = {10, 0};
    if (setsockopt(control, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout)) || setsockopt(control, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof(timeout))) goto cleanup;
    phase = PROXY_IDENTITY;
    pid_t peer = 0;
    uid_t peer_uid; gid_t peer_gid;
    socklen_t peer_size = sizeof(peer);
    if (getpeereid(control, &peer_uid, &peer_gid) || peer_uid != getuid() ||
        getsockopt(control, SOL_LOCAL, LOCAL_PEERPID, &peer, &peer_size) || peer_size != sizeof(peer)) goto cleanup;
    if (read_all(control, hello, sizeof(hello)) || hello[0] != (uint64_t)peer || !hello[0] || !hello[1]) goto cleanup;
    if (coalition_for((pid_t)hello[0], &actual) || actual != hello[1] || coalition_count(actual, &count) || count != 1) goto cleanup;
    owned_id = actual;
    phase = PROXY_ENVELOPE;
    int pipes[2] = {1, 2};
    char *cwd = getcwd(NULL, 0);
    if (!cwd) goto cleanup;
    uint32_t mask = (uint32_t)command_mask;
    failed = stopping || ended(0) || transfer_pipes(control, pipes, 1) || send_string(control, cwd) || write_all(control, &mask, sizeof(mask)) || send_vector(control, args) || send_vector(control, environ);
    free(cwd);
    if (failed) goto cleanup;
    phase = PROXY_WAIT;
    for (;;) {
        struct pollfd p = {control, POLLIN | POLLHUP | POLLERR, 0};
        if (poll(&p, 1, 10) > 0 && p.revents) {
            if (!read_all(control, &code, sizeof(code)) && code >= 0 && code <= 255) complete = 1;
            break;
        }
        if (stopping || ended(0)) {
            shutdown(control, SHUT_WR);
            /* Keep the read half until the independent owner proves cleanup. */
        }
    }
cleanup:
    if (control >= 0) close(control);
    close(listener);
    (void)launchctl("bootout", target, NULL);
    if (owned_id) {
        /* Recover a crashed supervisor using its kernel ownership domain. */
        for (;;) {
            if (coalition_count(owned_id, &count)) return 125;
            if (!count) break;
            if (signal_coalition(owned_id, 0, SIGKILL)) return 125;
            pause_tick();
        }
    }
    /* Revocation can arrive before the command envelope is released. The
     * domain has still been verified empty above, so acknowledge cleanup even
     * without a root status. Rust retains the independent timeout/cancel reason.
     * A supervisor crash likewise becomes a failed command after recovery. */
    return acknowledge(scope, complete ? code : 125);
}
#endif
int main(int argc, char **argv) {
    command_mask = umask(077); signals();
    if (argc < 3) return 125;
#ifdef __APPLE__
    if (!strcmp(argv[1], "supervise") && argc == 3) {
        int result = supervisor_mac(argv[2]);
        char path[512]; snprintf(path, sizeof(path), "%s/diagnostic", argv[2]);
        FILE *f = fopen(path, "a"); if (f) { fprintf(f, "role=supervisor phase=%d errno=%d\n", phase, errno); fclose(f); }
        return result;
    }
#endif
    if (argc < 4 || strcmp(argv[1], "run")) return 125;
#ifdef __APPLE__
    int result = run_mac(argv[2], argv[0], &argv[3]);
    if (result == 125) {
        char path[512]; snprintf(path, sizeof(path), "%s/diagnostic", argv[2]);
        FILE *f = fopen(path, "a"); if (f) { fprintf(f, "role=proxy phase=%d errno=%d\n", phase, errno); fclose(f); }
    }
    return result;
#else
    return run_linux(argv[2], &argv[3]);
#endif
}
