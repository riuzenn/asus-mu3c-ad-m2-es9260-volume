package flash

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/firmware"
)

const EEPROMSize = 32768

// FeatureIO exchanges 64-byte HID feature payloads. Report ID zero is added by
// the OS-specific transport and is not included here.
type FeatureIO interface {
	SetFeature([]byte) error
	GetFeature() ([]byte, error)
}

type Progress func(string)

func report(progress Progress, message string) {
	if progress != nil {
		progress(message)
	}
}

func requestResponse(device FeatureIO, command []byte) ([]byte, error) {
	if err := device.SetFeature(command); err != nil {
		return nil, err
	}
	return device.GetFeature()
}

func ReadEEPROM(device FeatureIO) ([]byte, error) {
	result := make([]byte, EEPROMSize)
	for address := 0; address < EEPROMSize; address += 64 {
		command := []byte{0x15, byte(address >> 8), byte(address), 64}
		var block []byte
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			block, err = requestResponse(device, command)
			if err == nil && len(block) >= 64 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil || len(block) < 64 {
			return nil, fmt.Errorf("EEPROM read failed at 0x%04x: %v", address, err)
		}
		copy(result[address:address+64], block[:64])
		time.Sleep(2 * time.Millisecond)
	}
	return result, nil
}

func FlashType(device FeatureIO) (byte, error) {
	for attempt := 0; attempt < 5; attempt++ {
		response, err := requestResponse(device, []byte{0x29, 1})
		if err == nil && len(response) > 0 {
			return response[0], nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0, errors.New("could not read SPI flash type")
}

func waitReady(device FeatureIO) error {
	for poll := 0; poll < 1000; poll++ {
		response, err := requestResponse(device, []byte{0x2d, 1})
		if err == nil && len(response) > 0 && response[0] != 0 {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return errors.New("SPI flash did not become ready")
}

func writeChunk(device FeatureIO, address int, data []byte, wait bool) error {
	if len(data) < 1 || len(data) > 60 || address < 0 || address > 0xffff {
		return errors.New("invalid SPI write chunk")
	}
	command := make([]byte, 4+len(data))
	command[0] = 0x17
	command[1] = byte(address >> 8)
	command[2] = byte(address)
	command[3] = byte(len(data))
	copy(command[4:], data)
	for attempt := 0; attempt < 3; attempt++ {
		if err := device.SetFeature(command); err == nil {
			if !wait || waitReady(device) == nil {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("SPI write failed at 0x%04x", address)
}

func writeROM(device FeatureIO, rom []byte, progress Progress) error {
	for address := 0; address < len(rom); {
		chunk := len(rom) - address
		if chunk > 60 {
			chunk = 60
		}
		if pageRemaining := 256 - address%256; chunk > pageRemaining {
			chunk = pageRemaining
		}
		if err := writeChunk(device, address, rom[address:address+chunk], true); err != nil {
			return err
		}
		address += chunk
		if address%4096 < chunk || address == len(rom) {
			report(progress, fmt.Sprintf("programmed %d/%d ROM bytes", address, len(rom)))
		}
	}
	return nil
}

func restoreTail(device FeatureIO, actual, target []byte) error {
	for address := firmware.ROMSize; address < EEPROMSize; {
		if actual[address] == target[address] {
			address++
			continue
		}
		if target[address] == 0xff {
			return fmt.Errorf("tail byte 0x%04x cannot be restored without erase", address)
		}
		start := address
		for address < EEPROMSize && address-start < 60 && address-start < 256-start%256 &&
			actual[address] != target[address] && target[address] != 0xff {
			address++
		}
		if err := writeChunk(device, start, target[start:address], true); err != nil {
			return err
		}
	}
	return nil
}

func compare(actual, expected []byte, stage string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%s length mismatch", stage)
	}
	if bytes.Equal(actual, expected) {
		return nil
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return fmt.Errorf("%s differs at 0x%04x (%02x != %02x)", stage, index, actual[index], expected[index])
		}
	}
	return errors.New("unknown compare error")
}

// Install performs the same staged header commit and full 32 KiB comparisons as
// the validated Linux flasher. All authorization and backup persistence checks
// must have completed before the caller reaches this function.
func Install(device FeatureIO, rom, backup []byte, progress Progress) ([]byte, error) {
	if len(rom) != firmware.ROMSize || firmware.Hash(rom) != firmware.V3SHA256 {
		return nil, errors.New("ROM does not match the validated v3 hash")
	}
	if len(backup) != EEPROMSize {
		return nil, errors.New("backup must be exactly 32 KiB")
	}
	if rom[0] != 0x45 || rom[4] != 6 || rom[5] != 3 || rom[10] != 14 {
		return nil, errors.New("ROM header/version mismatch")
	}
	var checksum uint32
	for _, value := range rom[0x10:] {
		checksum += uint32(value)
	}
	if binary.LittleEndian.Uint16(rom[2:4]) != uint16(checksum) {
		return nil, errors.New("ROM checksum mismatch")
	}
	flashType, err := FlashType(device)
	if err != nil || flashType != 1 {
		return nil, fmt.Errorf("expected SPI flash type 1, got %d: %v", flashType, err)
	}
	live, err := ReadEEPROM(device)
	if err != nil {
		return nil, err
	}
	if err := compare(live, backup, "live EEPROM / session backup"); err != nil {
		return nil, err
	}
	report(progress, "preflight: live 32 KiB exactly matches backup")
	if err := writeChunk(device, 0x8000, []byte{live[5]}, false); err != nil {
		return nil, fmt.Errorf("store current major version: %w", err)
	}
	time.Sleep(5 * time.Millisecond)
	report(progress, "erasing firmware storage")
	if err := device.SetFeature([]byte{0x2b, 0, 0, 1}); err != nil {
		return nil, fmt.Errorf("SPI erase: %w", err)
	}
	if err := waitReady(device); err != nil {
		return nil, fmt.Errorf("SPI erase: %w", err)
	}
	staged := append([]byte(nil), rom...)
	staged[0] = 0xff
	if err := writeROM(device, staged, progress); err != nil {
		return nil, fmt.Errorf("staged ROM programming; header remains uncommitted: %w", err)
	}
	live, err = ReadEEPROM(device)
	if err != nil {
		return nil, fmt.Errorf("staged readback; header remains uncommitted: %w", err)
	}
	if err := compare(live[:len(staged)], staged, "staged ROM"); err != nil {
		return nil, err
	}
	target := append(append([]byte(nil), rom...), backup[len(rom):]...)
	if err := restoreTail(device, live, target); err != nil {
		return nil, fmt.Errorf("configuration-tail restoration: %w", err)
	}
	live, err = ReadEEPROM(device)
	if err != nil {
		return nil, fmt.Errorf("pre-commit readback: %w", err)
	}
	copy(target, staged)
	if err := compare(live, target, "pre-commit 32 KiB"); err != nil {
		return nil, err
	}
	report(progress, "pre-commit 32 KiB matches target")
	if err := writeChunk(device, 0, rom[:1], true); err != nil {
		return nil, fmt.Errorf("header commit: %w", err)
	}
	time.Sleep(20 * time.Millisecond)
	live, err = ReadEEPROM(device)
	if err != nil {
		return nil, fmt.Errorf("final readback: %w", err)
	}
	copy(target, rom)
	if err := compare(live, target, "final 32 KiB"); err != nil {
		return nil, err
	}
	report(progress, "FLASH COMPLETE: final 32 KiB matches v3 ROM and preserved configuration tail")
	return live, nil
}
