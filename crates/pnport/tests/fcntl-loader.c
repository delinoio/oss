// SPDX-License-Identifier: Apache-2.0
#include <dlfcn.h>
#include <stdio.h>

int main(int argc, char **argv) {
    if (argc != 2) return 3;
    void *handle = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (!handle) {
        fprintf(stderr, "fcntl fixture dlopen failed: %s\n", dlerror());
        return 4;
    }
    int (*result)(void) = dlsym(handle, "probe_result");
    int rc = result ? result() : 5;
    if (dlclose(handle) != 0) return 6;
    return rc;
}
