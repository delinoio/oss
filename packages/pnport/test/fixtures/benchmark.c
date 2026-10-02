// SPDX-License-Identifier: Apache-2.0
// Count fixture-level calls; these are not kernel syscall or device-I/O counts.
#include <dirent.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc != 3) return 2;
    char *end;
    long iterations = strtol(argv[2], &end, 10);
    if (*end || iterations < 1 || iterations > 100000) return 2;
    unsigned long long bytes = 0, entries = 0;
    volatile unsigned char observed = 0;
    for (long index = 0; index < iterations; index++) {
        struct stat metadata;
        if (stat(argv[1], &metadata) || metadata.st_size <= 0) return 3;
        int descriptor = open(argv[1], O_RDONLY);
        if (descriptor < 0) return 4;
        char buffer[8192];
        ssize_t count = read(descriptor, buffer, sizeof(buffer));
        if (count <= 0 || fstat(descriptor, &metadata)) return 5;
        bytes += (unsigned long long)count;
        void *mapping = mmap(NULL, (size_t)metadata.st_size, PROT_READ, MAP_PRIVATE, descriptor, 0);
        if (mapping == MAP_FAILED) return 6;
        observed ^= ((unsigned char *)mapping)[metadata.st_size - 1];
        if (munmap(mapping, (size_t)metadata.st_size) || close(descriptor)) return 7;
        char *canonical = realpath(argv[1], NULL);
        if (!canonical) return 8;
        free(canonical);
        DIR *directory = opendir("node_modules/@types/node");
        if (!directory) return 9;
        while (readdir(directory)) entries++;
        if (closedir(directory)) return 10;
    }
    (void)observed;
    printf("{\"iterations\":%ld,\"readBytes\":%llu,\"directoryEntries\":%llu,"
           "\"calls\":{\"stat\":%ld,\"open\":%ld,\"read\":%ld,\"fstat\":%ld,"
           "\"mmap\":%ld,\"munmap\":%ld,\"close\":%ld,\"realpath\":%ld,"
           "\"opendir\":%ld,\"closedir\":%ld}}\n",
           iterations, bytes, entries, iterations, iterations, iterations,
           iterations, iterations, iterations, iterations, iterations,
           iterations, iterations);
    return 0;
}
