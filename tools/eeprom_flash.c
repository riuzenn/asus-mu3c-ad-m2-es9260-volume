#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <fcntl.h>
#include <linux/hidraw.h>
#include <stdbool.h>
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
#define READ_BLOCK_BYTES 64
#define WRITE_BLOCK_BYTES 60
#define SPI_PAGE_BYTES 256
#define EXPECTED_ROM_BYTES 15400

static void pause_ms(long milliseconds) {
    struct timespec delay = {
        .tv_sec = milliseconds / 1000,
        .tv_nsec = (milliseconds % 1000) * 1000000L,
    };
    while (nanosleep(&delay, &delay) < 0 && errno == EINTR) {
    }
}

static int set_feature(int fd, const unsigned char *payload, size_t length) {
    unsigned char report[HID_BUFFER_BYTES];
    if (length > HID_BUFFER_BYTES - 1) {
        errno = EINVAL;
        return -1;
    }
    memset(report, 0, sizeof(report));
    report[0] = 0;
    memcpy(report + 1, payload, length);
    return ioctl(fd, HIDIOCSFEATURE(sizeof(report)), report) < 0 ? -1 : 0;
}

static int get_feature(int fd, unsigned char *payload, size_t length) {
    unsigned char report[HID_BUFFER_BYTES];
    if (length > HID_BUFFER_BYTES - 1) {
        errno = EINVAL;
        return -1;
    }
    memset(report, 0, sizeof(report));
    report[0] = 0;
    if (ioctl(fd, HIDIOCGFEATURE(sizeof(report)), report) < 0) {
        return -1;
    }
    memcpy(payload, report + 1, length);
    return 0;
}

static int read_block_once(int fd, uint16_t address, uint8_t length,
                           unsigned char *output) {
    unsigned char command[4] = {
        0x15,
        (uint8_t)(address >> 8),
        (uint8_t)(address & 0xff),
        length,
    };
    if (set_feature(fd, command, sizeof(command)) < 0) {
        return -1;
    }
    return get_feature(fd, output, length);
}

static int read_block(int fd, uint16_t address, uint8_t length,
                      unsigned char *output) {
    for (int attempt = 1; attempt <= 5; ++attempt) {
        if (read_block_once(fd, address, length, output) == 0) {
            return 0;
        }
        fprintf(stderr, "read 0x%04x attempt %d/5: %s\n", address, attempt,
                strerror(errno));
        pause_ms(20);
    }
    return -1;
}

static int read_range(int fd, uint16_t address, size_t length,
                      unsigned char *output) {
    size_t done = 0;
    while (done < length) {
        size_t remaining = length - done;
        uint8_t chunk = remaining < READ_BLOCK_BYTES ? (uint8_t)remaining
                                                     : READ_BLOCK_BYTES;
        if (read_block(fd, (uint16_t)(address + done), chunk, output + done) < 0) {
            return -1;
        }
        done += chunk;
        pause_ms(2);
    }
    return 0;
}

static int spi_check_ready_once(int fd, bool *ready) {
    unsigned char command[2] = {0x2d, 1};
    unsigned char response[64];
    if (set_feature(fd, command, sizeof(command)) < 0 ||
        get_feature(fd, response, sizeof(response)) < 0) {
        return -1;
    }
    *ready = response[0] > 0;
    return 0;
}

static int wait_spi_ready(int fd) {
    for (int poll = 0; poll < 1000; ++poll) {
        bool ready = false;
        if (spi_check_ready_once(fd, &ready) == 0) {
            if (ready) {
                return 0;
            }
        } else if ((poll % 50) == 0) {
            fprintf(stderr, "SPI-ready poll %d: %s\n", poll + 1,
                    strerror(errno));
        }
        pause_ms(2);
    }
    fprintf(stderr, "SPI did not become ready\n");
    return -1;
}

static int get_flash_type(int fd, uint8_t *flash_type) {
    unsigned char command[2] = {0x29, 1};
    unsigned char response[64];
    for (int attempt = 1; attempt <= 5; ++attempt) {
        if (set_feature(fd, command, sizeof(command)) == 0 &&
            get_feature(fd, response, sizeof(response)) == 0) {
            *flash_type = response[0];
            return 0;
        }
        fprintf(stderr, "flash-type attempt %d/5: %s\n", attempt,
                strerror(errno));
        pause_ms(20);
    }
    return -1;
}

static int write_chunk_once(int fd, uint16_t address,
                            const unsigned char *data, uint8_t length) {
    unsigned char command[4 + WRITE_BLOCK_BYTES];
    if (length == 0 || length > WRITE_BLOCK_BYTES) {
        errno = EINVAL;
        return -1;
    }
    command[0] = 0x17;
    command[1] = (uint8_t)(address >> 8);
    command[2] = (uint8_t)(address & 0xff);
    command[3] = length;
    memcpy(command + 4, data, length);
    return set_feature(fd, command, (size_t)length + 4);
}

static int write_chunk(int fd, uint16_t address, const unsigned char *data,
                       uint8_t length, bool wait_ready) {
    for (int attempt = 1; attempt <= 3; ++attempt) {
        if (write_chunk_once(fd, address, data, length) == 0) {
            if (!wait_ready || wait_spi_ready(fd) == 0) {
                return 0;
            }
        }
        fprintf(stderr, "write 0x%04x attempt %d/3: %s\n", address, attempt,
                strerror(errno));
        pause_ms(20);
    }
    return -1;
}

static int write_range_spi(int fd, uint16_t address,
                           const unsigned char *data, size_t length) {
    size_t done = 0;
    while (done < length) {
        uint16_t current = (uint16_t)(address + done);
        size_t remaining = length - done;
        size_t page_remaining = SPI_PAGE_BYTES - (current & 0xff);
        size_t chunk = remaining;
        if (chunk > WRITE_BLOCK_BYTES) {
            chunk = WRITE_BLOCK_BYTES;
        }
        if (chunk > page_remaining) {
            chunk = page_remaining;
        }
        if (write_chunk(fd, current, data + done, (uint8_t)chunk, true) < 0) {
            return -1;
        }
        done += chunk;
        if ((done % 4096) < chunk || done == length) {
            fprintf(stderr, "programmed %zu/%zu bytes\n", done, length);
        }
    }
    return 0;
}

static int spi_erase(int fd) {
    unsigned char command[4] = {0x2b, 0, 0, 1};
    if (set_feature(fd, command, sizeof(command)) < 0) {
        return -1;
    }
    return wait_spi_ready(fd);
}

static unsigned char *read_file_exact(const char *path, size_t expected_size) {
    FILE *file = fopen(path, "rb");
    if (file == NULL) {
        fprintf(stderr, "open %s: %s\n", path, strerror(errno));
        return NULL;
    }
    unsigned char *data = malloc(expected_size);
    if (data == NULL) {
        fprintf(stderr, "malloc: %s\n", strerror(errno));
        fclose(file);
        return NULL;
    }
    size_t got = fread(data, 1, expected_size, file);
    int extra = fgetc(file);
    if (got != expected_size || extra != EOF || ferror(file)) {
        fprintf(stderr, "%s must be exactly %zu bytes\n", path, expected_size);
        free(data);
        fclose(file);
        return NULL;
    }
    if (fclose(file) != 0) {
        fprintf(stderr, "close %s: %s\n", path, strerror(errno));
        free(data);
        return NULL;
    }
    return data;
}

static int write_file_exact(const char *path, const unsigned char *data,
                            size_t size) {
    FILE *file = fopen(path, "wb");
    if (file == NULL) {
        fprintf(stderr, "open %s: %s\n", path, strerror(errno));
        return -1;
    }
    int rc = 0;
    if (fwrite(data, 1, size, file) != size || fflush(file) != 0 ||
        fsync(fileno(file)) != 0) {
        fprintf(stderr, "write %s: %s\n", path, strerror(errno));
        rc = -1;
    }
    if (fclose(file) != 0) {
        fprintf(stderr, "close %s: %s\n", path, strerror(errno));
        rc = -1;
    }
    return rc;
}

static int validate_rom(const unsigned char *rom) {
    uint16_t stored = (uint16_t)rom[2] | ((uint16_t)rom[3] << 8);
    uint32_t sum = 0;
    for (size_t i = 0x10; i < EXPECTED_ROM_BYTES; ++i) {
        sum += rom[i];
    }
    uint16_t computed = (uint16_t)sum;
    if (rom[0] != 0x45 || rom[4] != 6 || rom[5] != 3 || rom[10] != 14 ||
        stored != computed) {
        fprintf(stderr,
                "refusing ROM: header/version/checksum mismatch "
                "(header=%02x version=%u.%u.%u stored=%04x computed=%04x)\n",
                rom[0], rom[5], rom[4], rom[10], stored, computed);
        return -1;
    }
    return 0;
}

static int first_mismatch(const unsigned char *a, const unsigned char *b,
                          size_t length, size_t *offset) {
    for (size_t i = 0; i < length; ++i) {
        if (a[i] != b[i]) {
            *offset = i;
            return 1;
        }
    }
    return 0;
}

static int restore_tail_if_needed(int fd, const unsigned char *target,
                                  unsigned char *actual) {
    size_t offset = EXPECTED_ROM_BYTES;
    while (offset < EEPROM_BYTES) {
        if (actual[offset] == target[offset]) {
            ++offset;
            continue;
        }
        if (target[offset] == 0xff) {
            fprintf(stderr,
                    "cannot restore tail at 0x%04zx without another erase: "
                    "actual=%02x target=ff\n",
                    offset, actual[offset]);
            return -1;
        }
        size_t start = offset;
        size_t length = 0;
        while (offset < EEPROM_BYTES && length < WRITE_BLOCK_BYTES &&
               length < SPI_PAGE_BYTES - (start & 0xff) &&
               actual[offset] != target[offset] && target[offset] != 0xff) {
            ++offset;
            ++length;
        }
        fprintf(stderr, "restoring config bytes 0x%04zx..0x%04zx\n", start,
                start + length - 1);
        if (write_chunk(fd, (uint16_t)start, target + start, (uint8_t)length,
                        true) < 0) {
            return -1;
        }
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 5) {
        fprintf(stderr,
                "usage: %s /dev/hidrawN PATCHED.rom FULL_BACKUP.bin "
                "POST_FLASH_READ.bin\n",
                argv[0]);
        return 2;
    }

    unsigned char *rom = read_file_exact(argv[2], EXPECTED_ROM_BYTES);
    unsigned char *backup = read_file_exact(argv[3], EEPROM_BYTES);
    if (rom == NULL || backup == NULL || validate_rom(rom) < 0) {
        free(rom);
        free(backup);
        return 1;
    }

    unsigned char *live = malloc(EEPROM_BYTES);
    unsigned char *target = malloc(EEPROM_BYTES);
    unsigned char *staged = malloc(EXPECTED_ROM_BYTES);
    if (live == NULL || target == NULL || staged == NULL) {
        fprintf(stderr, "malloc: %s\n", strerror(errno));
        free(rom);
        free(backup);
        free(live);
        free(target);
        free(staged);
        return 1;
    }
    memcpy(target, rom, EXPECTED_ROM_BYTES);
    memcpy(target + EXPECTED_ROM_BYTES, backup + EXPECTED_ROM_BYTES,
           EEPROM_BYTES - EXPECTED_ROM_BYTES);
    memcpy(staged, rom, EXPECTED_ROM_BYTES);
    staged[0] = 0xff; /* Make interrupted programming unbootable until verified. */

    int fd = open(argv[1], O_RDWR | O_CLOEXEC);
    if (fd < 0) {
        fprintf(stderr, "open %s: %s\n", argv[1], strerror(errno));
        goto fail;
    }
    struct hidraw_devinfo info;
    memset(&info, 0, sizeof(info));
    if (ioctl(fd, HIDIOCGRAWINFO, &info) < 0 || info.vendor != VID ||
        info.product != PID) {
        fprintf(stderr, "refusing HID device; expected %04x:%04x\n", VID, PID);
        close(fd);
        goto fail;
    }

    uint8_t flash_type = 0;
    if (get_flash_type(fd, &flash_type) < 0 || flash_type != 1) {
        fprintf(stderr, "refusing device: expected SPI flash type 1, got %u\n",
                flash_type);
        close(fd);
        goto fail;
    }
    fprintf(stderr, "preflight: %04hx:%04hx, SPI type %u\n", info.vendor,
            info.product, flash_type);

    if (read_range(fd, 0, EEPROM_BYTES, live) < 0) {
        fprintf(stderr, "preflight EEPROM read failed\n");
        close(fd);
        goto fail;
    }
    size_t mismatch = 0;
    if (first_mismatch(live, backup, EEPROM_BYTES, &mismatch)) {
        fprintf(stderr,
                "refusing flash: live EEPROM differs from supplied backup at "
                "0x%04zx (%02x != %02x)\n",
                mismatch, live[mismatch], backup[mismatch]);
        close(fd);
        goto fail;
    }
    fprintf(stderr, "preflight: live EEPROM exactly matches 32 KiB backup\n");

    uint8_t data_major = live[5];
    if (write_chunk(fd, 0x8000, &data_major, 1, false) < 0) {
        fprintf(stderr, "failed to store current major version at 0x8000\n");
        close(fd);
        goto fail;
    }
    pause_ms(5);

    fprintf(stderr, "erasing firmware storage...\n");
    if (spi_erase(fd) < 0) {
        fprintf(stderr, "SPI erase failed\n");
        close(fd);
        goto fail;
    }

    fprintf(stderr, "programming staged ROM with byte 0 left erased...\n");
    if (write_range_spi(fd, 0, staged, EXPECTED_ROM_BYTES) < 0) {
        fprintf(stderr, "ROM programming failed; byte 0 remains uncommitted\n");
        close(fd);
        goto fail;
    }

    if (read_range(fd, 0, EEPROM_BYTES, live) < 0) {
        fprintf(stderr, "staged readback failed; byte 0 remains uncommitted\n");
        close(fd);
        goto fail;
    }
    if (first_mismatch(live, staged, EXPECTED_ROM_BYTES, &mismatch)) {
        fprintf(stderr,
                "staged ROM compare failed at 0x%04zx (%02x != %02x); "
                "byte 0 remains uncommitted\n",
                mismatch, live[mismatch], staged[mismatch]);
        close(fd);
        goto fail;
    }
    fprintf(stderr, "staged ROM readback matches exactly\n");

    if (restore_tail_if_needed(fd, target, live) < 0) {
        fprintf(stderr, "configuration-tail restoration failed\n");
        close(fd);
        goto fail;
    }
    if (read_range(fd, 0, EEPROM_BYTES, live) < 0) {
        fprintf(stderr, "pre-commit full EEPROM read failed\n");
        close(fd);
        goto fail;
    }
    memcpy(target, staged, EXPECTED_ROM_BYTES);
    if (first_mismatch(live, target, EEPROM_BYTES, &mismatch)) {
        fprintf(stderr,
                "pre-commit full compare failed at 0x%04zx (%02x != %02x); "
                "byte 0 remains uncommitted\n",
                mismatch, live[mismatch], target[mismatch]);
        close(fd);
        goto fail;
    }
    fprintf(stderr, "pre-commit 32 KiB image matches target\n");

    fprintf(stderr, "committing firmware header byte...\n");
    if (write_chunk(fd, 0, rom, 1, true) < 0) {
        fprintf(stderr, "header commit failed\n");
        close(fd);
        goto fail;
    }
    pause_ms(20);

    if (read_range(fd, 0, EEPROM_BYTES, live) < 0) {
        fprintf(stderr, "final EEPROM readback failed\n");
        close(fd);
        goto fail;
    }
    memcpy(target, rom, EXPECTED_ROM_BYTES);
    if (first_mismatch(live, target, EEPROM_BYTES, &mismatch)) {
        fprintf(stderr, "final compare failed at 0x%04zx (%02x != %02x)\n",
                mismatch, live[mismatch], target[mismatch]);
        close(fd);
        goto fail;
    }
    if (write_file_exact(argv[4], live, EEPROM_BYTES) < 0) {
        close(fd);
        goto fail;
    }

    fprintf(stderr,
            "FLASH COMPLETE: final 32 KiB readback matches patched ROM plus "
            "preserved configuration tail\n");
    close(fd);
    free(rom);
    free(backup);
    free(live);
    free(target);
    free(staged);
    return 0;

fail:
    free(rom);
    free(backup);
    free(live);
    free(target);
    free(staged);
    return 1;
}
