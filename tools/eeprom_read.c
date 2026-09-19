#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <fcntl.h>
#include <linux/hidraw.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <time.h>
#include <unistd.h>

#define VID 0x0495
#define PID 0x3042
#define HID_BUFFER_BYTES 65
#define EEPROM_BYTES 32768
#define BLOCK_BYTES 64

static void pause_ms(long milliseconds) {
    struct timespec delay = {
        .tv_sec = milliseconds / 1000,
        .tv_nsec = (milliseconds % 1000) * 1000000L,
    };
    while (nanosleep(&delay, &delay) < 0 && errno == EINTR) {
    }
}

static int read_block(int fd, uint16_t address, uint8_t length,
                      unsigned char *output) {
    unsigned char report[HID_BUFFER_BYTES];
    memset(report, 0, sizeof(report));
    report[0] = 0;       /* report-ID slot required by hidraw ioctl */
    report[1] = 0x15;    /* ASUS updater EEPROM read opcode */
    report[2] = (uint8_t)(address >> 8);
    report[3] = (uint8_t)(address & 0xff);
    report[4] = length;

    if (ioctl(fd, HIDIOCSFEATURE(sizeof(report)), report) < 0) {
        fprintf(stderr, "EEPROM read command at 0x%04x: %s\n", address,
                strerror(errno));
        return -1;
    }

    memset(report, 0, sizeof(report));
    report[0] = 0;
    if (ioctl(fd, HIDIOCGFEATURE(sizeof(report)), report) < 0) {
        fprintf(stderr, "EEPROM read response at 0x%04x: %s\n", address,
                strerror(errno));
        return -1;
    }
    memcpy(output, report + 1, length);
    return 0;
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
    if (ioctl(fd, HIDIOCGRAWINFO, &info) < 0) {
        fprintf(stderr, "HIDIOCGRAWINFO: %s\n", strerror(errno));
        close(fd);
        return 1;
    }
    if (info.vendor != VID || info.product != PID) {
        fprintf(stderr, "refusing HID %04hx:%04hx; expected %04x:%04x\n",
                info.vendor, info.product, VID, PID);
        close(fd);
        return 1;
    }
    fprintf(stderr, "reading %04hx:%04hx from %s\n", info.vendor, info.product,
            argv[1]);

    unsigned char *eeprom = malloc(EEPROM_BYTES);
    if (eeprom == NULL) {
        fprintf(stderr, "malloc: %s\n", strerror(errno));
        close(fd);
        return 1;
    }

    int rc = 0;
    for (unsigned int address = 0; address < EEPROM_BYTES; address += BLOCK_BYTES) {
        int success = 0;
        for (int attempt = 1; attempt <= 3; ++attempt) {
            if (read_block(fd, (uint16_t)address, BLOCK_BYTES, eeprom + address) == 0) {
                success = 1;
                break;
            }
            fprintf(stderr, "retry %d/3 for address 0x%04x\n", attempt, address);
            pause_ms(20);
        }
        if (!success) {
            fprintf(stderr, "read stopped at EEPROM address 0x%04x\n", address);
            rc = 1;
            goto cleanup;
        }
        if ((address & 0x0fff) == 0) {
            fprintf(stderr, "read 0x%04x/0x%04x bytes\n", address, EEPROM_BYTES);
        }
        pause_ms(2);
    }

    FILE *output = fopen(argv[2], "wb");
    if (output == NULL) {
        fprintf(stderr, "open output: %s\n", strerror(errno));
        rc = 1;
        goto cleanup;
    }
    if (fwrite(eeprom, 1, EEPROM_BYTES, output) != EEPROM_BYTES) {
        fprintf(stderr, "write output: %s\n", strerror(errno));
        rc = 1;
    }
    if (fclose(output) != 0) {
        fprintf(stderr, "close output: %s\n", strerror(errno));
        rc = 1;
    }
    if (rc == 0) {
        fprintf(stderr, "EEPROM read complete: %d bytes\n", EEPROM_BYTES);
    }

cleanup:
    free(eeprom);
    close(fd);
    return rc;
}
