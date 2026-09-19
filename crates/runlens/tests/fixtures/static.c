/* A static Linux child must be observed by seccomp, without LD_PRELOAD. */
#include <stdio.h>
int main(void) {
    FILE *input = fopen("input.txt", "rb");
    if (!input) return 21;
    int value = fgetc(input);
    fclose(input);
    FILE *output = fopen("static-output.txt", "wb");
    if (!output) return 22;
    fputc(value, output);
    return fclose(output) != 0;
}
