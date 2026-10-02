#define _GNU_SOURCE
#include <errno.h>
#include <pthread.h>
#include <signal.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>

#define CHECK(condition) do { if (!(condition)) { fprintf(stderr, "receive check line=%d errno=%d\n", __LINE__, errno); exit(1); } } while (0)

static int sender;
static volatile sig_atomic_t alarms;
static void alarm_handler(int signal) { (void)signal; alarms++; }
static void *send_later(void *unused) {
    (void)unused;
    usleep(150000);
    CHECK(send(sender, "p", 1, 0) == 1);
    return NULL;
}

int main(int argc, char **argv) {
    CHECK(argc == 3);
    int kind = !strcmp(argv[1], "stream") ? SOCK_STREAM :
        !strcmp(argv[1], "packet") ? SOCK_SEQPACKET : SOCK_DGRAM;
    int sockets[2];
    CHECK(socketpair(AF_UNIX, kind, 0, sockets) == 0);
    char byte = 0;
    struct iovec data = {.iov_base = &byte, .iov_len = 1};
    unsigned char control[128], expected[128];
    memset(control, 0xa5, sizeof(control));
    memcpy(expected, control, sizeof(control));
    struct sockaddr_storage address;
    struct msghdr message = {.msg_name = &address, .msg_namelen = sizeof(address),
        .msg_iov = &data, .msg_iovlen = 1, .msg_control = control,
        .msg_controllen = sizeof(control), .msg_flags = 0x1234};

    if (!strcmp(argv[2], "eagain")) {
        CHECK(recvmsg(sockets[1], &message, MSG_DONTWAIT) == -1 && errno == EAGAIN);
        CHECK(message.msg_controllen == sizeof(control) && message.msg_flags == 0x1234);
    }
    pthread_t thread;
    int delayed = !strcmp(argv[2], "blocked") || !strcmp(argv[2], "restart") || !strcmp(argv[2], "interrupt");
    if (delayed) {
        sender = sockets[0];
        sigset_t blocked, previous;
        CHECK(sigemptyset(&blocked) == 0 && sigaddset(&blocked, SIGALRM) == 0);
        CHECK(pthread_sigmask(SIG_BLOCK, &blocked, &previous) == 0);
        if (strcmp(argv[2], "blocked")) {
            struct sigaction action = {.sa_handler = alarm_handler,
                .sa_flags = !strcmp(argv[2], "restart") ? SA_RESTART : 0};
            CHECK(sigaction(SIGALRM, &action, NULL) == 0);
        }
        // The sender inherits the blocked signal so only the receiving
        // thread can handle the interruption/restart probe.
        CHECK(pthread_create(&thread, NULL, send_later, NULL) == 0);
        CHECK(pthread_sigmask(SIG_SETMASK, &previous, NULL) == 0);
        if (strcmp(argv[2], "blocked")) {
            struct itimerval timer = {.it_value = {.tv_usec = 50000}};
            CHECK(setitimer(ITIMER_REAL, &timer, NULL) == 0);
        }
    } else {
        CHECK(send(sockets[0], "p", 1, 0) == 1);
    }
    if (!strcmp(argv[2], "fault")) {
        data.iov_base = (void *)1;
        CHECK(recvmsg(sockets[1], &message, 0) == -1 && errno == EFAULT);
        CHECK(message.msg_controllen == sizeof(control) && message.msg_flags == 0x1234);
    } else if (!strcmp(argv[2], "partial-header")) {
        size_t size = sysconf(_SC_PAGESIZE);
        char *mapping = mmap(NULL, 2 * size, PROT_READ | PROT_WRITE,
            MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
        CHECK(mapping != MAP_FAILED);
        struct msghdr *partial = (struct msghdr *)(mapping + size - offsetof(struct msghdr, msg_control));
        *partial = message;
        partial->msg_name = NULL;
        partial->msg_namelen = 0x1234;
        CHECK(mprotect(mapping, size, PROT_READ) == 0);
        CHECK(recvmsg(sockets[1], partial, 0) == 1 && byte == 'p');
        CHECK(partial->msg_name == NULL && partial->msg_namelen == 0x1234);
        CHECK(partial->msg_control == control && partial->msg_controllen == 0);
        CHECK(!(partial->msg_flags & MSG_CTRUNC));
        CHECK(munmap(mapping, 2 * size) == 0);
    } else if (!strcmp(argv[2], "readonly")) {
        size_t size = sysconf(_SC_PAGESIZE);
        struct msghdr *readonly = mmap(NULL, size, PROT_READ | PROT_WRITE,
            MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
        CHECK(readonly != MAP_FAILED);
        *readonly = message;
        CHECK(mprotect(readonly, size, PROT_READ) == 0);
        CHECK(recvmsg(sockets[1], readonly, 0) == -1 && errno == EFAULT);
        CHECK(readonly->msg_control == control && readonly->msg_controllen == sizeof(control));
        CHECK(byte == 'p' && munmap(readonly, size) == 0);
    } else {
        int peek = !strcmp(argv[2], "peek");
        ssize_t result = recvmsg(sockets[1], &message, peek ? MSG_PEEK : 0);
        if (!strcmp(argv[2], "interrupt")) {
            CHECK(result == -1 && errno == EINTR && alarms == 1);
            CHECK(message.msg_controllen == sizeof(control) && message.msg_flags == 0x1234);
            CHECK(pthread_join(thread, NULL) == 0);
            delayed = 0;
            result = recvmsg(sockets[1], &message, 0);
        }
        CHECK(result == 1 && byte == 'p');
        CHECK(message.msg_name == &address && message.msg_iov == &data && message.msg_iovlen == 1);
        CHECK(message.msg_namelen == 0);
        CHECK(message.msg_control == control && message.msg_controllen == 0);
        CHECK(!(message.msg_flags & MSG_CTRUNC));
        if (peek) {
            message.msg_controllen = sizeof(control);
            byte = 0;
            CHECK(recvmsg(sockets[1], &message, 0) == 1 && byte == 'p');
            CHECK(message.msg_controllen == 0);
        }
    }
    if (delayed) CHECK(pthread_join(thread, NULL) == 0);
    if (!strcmp(argv[2], "restart")) CHECK(alarms == 1);
    CHECK(!memcmp(control, expected, sizeof(control)));
    CHECK(close(sockets[0]) == 0 && close(sockets[1]) == 0);
    puts("recvmsg-ok");
    return 0;
}
