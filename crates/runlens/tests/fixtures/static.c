/* A static Linux child must be observed by seccomp, without LD_PRELOAD. */
#define _GNU_SOURCE
#include <stdio.h>
#include <fcntl.h>
#include <string.h>
#include <unistd.h>
#include <sys/syscall.h>
int main(int argc, char **argv) {
    if (argc == 5) {
        int destination = open(argv[4], O_RDONLY | O_DIRECTORY);
        if (destination < 0) return 23;
        long result;
#ifdef SYS_rename
        if (!strcmp(argv[1], "rename")) {
            char path[4096];
            snprintf(path, sizeof(path), "%s/%s", argv[4], argv[3]);
            result = syscall(SYS_rename, argv[2], path);
        } else
#endif
        if (!strcmp(argv[1], "renameat")) {
            result = syscall(SYS_renameat, AT_FDCWD, argv[2], destination, argv[3]);
        } else {
            result = syscall(SYS_renameat2, AT_FDCWD, argv[2], destination, argv[3], 0);
        }
        close(destination);
        return (result == 0) != (!strcmp(argv[2], "input.txt"));
    }
    FILE *input = fopen("input.txt", "rb");
    if (!input) return 21;
    int value = fgetc(input);
    fclose(input);
    FILE *output = fopen("static-output.txt", "wb");
    if (!output) return 22;
    fputc(value, output);
    return fclose(output) != 0;
}
