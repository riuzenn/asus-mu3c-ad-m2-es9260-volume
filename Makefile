CC ?= cc
CPPFLAGS ?=
CFLAGS ?= -O2 -Wall -Wextra -Wpedantic
LDFLAGS ?=

TOOLS := build/eeprom-read build/eeprom-flash build/register-dump

.PHONY: all clean

all: $(TOOLS)

build:
	mkdir -p $@

build/eeprom-read: tools/eeprom_read.c | build
	$(CC) $(CPPFLAGS) $(CFLAGS) $< $(LDFLAGS) -o $@

build/eeprom-flash: tools/eeprom_flash.c | build
	$(CC) $(CPPFLAGS) $(CFLAGS) $< $(LDFLAGS) -o $@

build/register-dump: tools/register_dump.c | build
	$(CC) $(CPPFLAGS) $(CFLAGS) $< $(LDFLAGS) -o $@

clean:
	rm -f $(TOOLS)
