// A real libc variadic call above the old 4096-pointer interposer limit.
#include <assert.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
extern char **environ;
#define A8 "", "", "", "", "", "", "", ""
#define A64 A8, A8, A8, A8, A8, A8, A8, A8
#define A512 A64, A64, A64, A64, A64, A64, A64, A64
#define A4096 A512, A512, A512, A512, A512, A512, A512, A512
#define ARGS argv[0], "child", A4096, "last", (char *)NULL
int main(int argc, char **argv) {
    if (argc > 2 && strcmp(argv[1], "child") == 0) {
        assert(argc == 4099 && strcmp(argv[argc - 1], "last") == 0);
        FILE *file = fopen("input.txt", "r");
        assert(file && fclose(file) == 0);
        puts("4099 arguments preserved");
        return 0;
    }
    assert(argc == 2);
    if (strcmp(argv[1], "execl") == 0) execl(argv[0], ARGS);
    else if (strcmp(argv[1], "execlp") == 0) execlp(argv[0], ARGS);
    else if (strcmp(argv[1], "execle") == 0) execle(argv[0], ARGS, environ);
    else {
        long limit = sysconf(_SC_ARG_MAX);
        assert(limit > 0);
        char *large = malloc((size_t)limit + 1);
        assert(large);
        memset(large, 'x', (size_t)limit);
        large[limit] = 0;
        assert(execl(argv[0], argv[0], large, (char *)NULL) == -1);
        assert(errno == E2BIG);
        free(large);
        puts("kernel E2BIG preserved");
        return 0;
    }
    perror("variadic exec");
    return 1;
}
