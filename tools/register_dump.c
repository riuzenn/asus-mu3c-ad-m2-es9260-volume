#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <fcntl.h>
#include <linux/hidraw.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/ioctl.h>
#include <time.h>
#include <unistd.h>

#define VID 0x0495
#define PID 0x3042
#define REPORT_BYTES 65

static void pause_ms(long milliseconds) {
    struct timespec delay = {
        .tv_sec = milliseconds / 1000,
        .tv_nsec = (milliseconds % 1000) * 1000000L,
    };
    while (nanosleep(&delay, &delay) < 0 && errno == EINTR) {
    }
}

static int read_register_once(int fd, uint8_t address, uint8_t *value) {
    unsigned char report[REPORT_BYTES];
    memset(report, 0, sizeof(report));
    report[0] = 0;
    report[1] = 0x1d;
    report[2] = 0x80;
    report[3] = address;
    report[4] = 1;
    if (ioctl(fd, HIDIOCSFEATURE(sizeof(report)), report) < 0) {
        return -1;
    }
    memset(report, 0, sizeof(report));
    report[0] = 0;
    if (ioctl(fd, HIDIOCGFEATURE(sizeof(report)), report) < 0) {
        return -1;
    }
    *value = report[1];
    return 0;
}

static int read_register(int fd, uint8_t address, uint8_t *value) {
    for (int attempt = 1; attempt <= 5; ++attempt) {
        if (read_register_once(fd, address, value) == 0) {
            return 0;
        }
        fprintf(stderr, "read register 0x%02x attempt %d/5: %s\n", address,
                attempt, strerror(errno));
        pause_ms(20);
    }
    return -1;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: %s /dev/hidrawN OUTPUT.bin\n", argv[0]);
        return 2;
    }
    int fd = open(argv[1], O_RDWR | O_CLOEXEC);
    if (fd < 0) {
        fprintf(stderr, "open %s: %s\n", argv[1], strerror(errno));
        return 1;
    }
    struct hidraw_devinfo info;
    memset(&info, 0, sizeof(info));
    if (ioctl(fd, HIDIOCGRAWINFO, &info) < 0 || info.vendor != VID ||
        info.product != PID) {
        fprintf(stderr, "refusing HID device; expected %04x:%04x\n", VID, PID);
        close(fd);
        return 1;
    }
    unsigned char registers[256];
    for (unsigned int address = 0; address < sizeof(registers); ++address) {
        if (read_register(fd, (uint8_t)address, &registers[address]) < 0) {
            close(fd);
            return 1;
        }
        pause_ms(2);
    }
    FILE *output = fopen(argv[2], "wb");
    if (output == NULL || fwrite(registers, 1, sizeof(registers), output) !=
                              sizeof(registers) ||
        fclose(output) != 0) {
        fprintf(stderr, "write %s: %s\n", argv[2], strerror(errno));
        close(fd);
        return 1;
    }
    fprintf(stderr, "register dump complete: 256 bytes\n");
    close(fd);
    return 0;
}
