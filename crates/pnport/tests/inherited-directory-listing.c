// SPDX-License-Identifier: Apache-2.0
#define _GNU_SOURCE

#include <stdint.h>
#include <string.h>
#include <sys/syscall.h>
#include <unistd.h>

static int count_virtual_entry(int fd) {
    unsigned char buffer[4096];
    int count = 0;
    for (;;) {
        long size = syscall(SYS_getdents64, fd, buffer, sizeof(buffer));
        if (size < 0) return -1;
        if (size == 0) return count;
        for (long offset = 0; offset < size;) {
            uint16_t length;
            memcpy(&length, buffer + offset + 16, sizeof(length));
            if (length < 19 || offset + length > size) return -2;
            if (!strcmp((char *)buffer + offset + 19, "node_modules")) count++;
            offset += length;
        }
    }
}

int main(void) {
    int first = count_virtual_entry(9);
    int second = count_virtual_entry(10);
    return first < 0 || second < 0 || first + second != 1;
}
