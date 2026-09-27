package flash

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/firmware"
)

type mockSPI struct {
	memory        []byte
	next          []byte
	erases        int
	corruptStage  bool
	romProgrammed bool
}

func (mock *mockSPI) SetFeature(payload []byte) error {
	if len(payload) == 0 {
		return errors.New("empty command")
	}
	switch payload[0] {
	case 0x15:
		address := int(payload[1])<<8 | int(payload[2])
		length := int(payload[3])
		mock.next = append([]byte(nil), mock.memory[address:address+length]...)
		if mock.corruptStage && mock.erases > 0 && mock.romProgrammed && address == 0 {
			mock.next[1] ^= 1
		}
	case 0x29, 0x2d:
		mock.next = []byte{1}
	case 0x2b:
		mock.erases++
		for i := range mock.memory {
			mock.memory[i] = 0xff
		}
	case 0x17:
		address := int(payload[1])<<8 | int(payload[2])
		length := int(payload[3])
		if address == 0x8000 {
			return nil
		}
		for i := 0; i < length; i++ {
			mock.memory[address+i] &= payload[4+i]
		}
		if address+length == firmware.ROMSize {
			mock.romProgrammed = true
		}
	default:
		return errors.New("unknown command")
	}
	return nil
}

func (mock *mockSPI) GetFeature() ([]byte, error) {
	return append([]byte(nil), mock.next...), nil
}

func TestInstallWithSimulatedSPI(t *testing.T) {
	path := os.Getenv("MU3C_OFFICIAL_ROM")
	if path == "" {
		t.Skip("set MU3C_OFFICIAL_ROM to the owner-supplied official firmware")
	}
	base, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	target, err := firmware.PatchV3(base)
	if err != nil {
		t.Fatal(err)
	}
	backup := make([]byte, EEPROMSize)
	copy(backup, base)
	for i := len(base); i < len(backup); i++ {
		backup[i] = 0xff
	}
	backup[0x7ff8] = 0x12
	backup[0x7ff9] = 0x34
	mock := &mockSPI{memory: append([]byte(nil), backup...)}
	readback, err := Install(mock, target, backup, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mock.erases != 1 || !bytes.Equal(readback[:len(target)], target) ||
		!bytes.Equal(readback[len(target):], backup[len(target):]) {
		t.Fatal("staged flash did not produce exact ROM plus preserved EEPROM tail")
	}

	mock = &mockSPI{memory: append([]byte(nil), backup...)}
	badBackup := append([]byte(nil), backup...)
	badBackup[100] ^= 1
	if _, err := Install(mock, target, badBackup, nil); err == nil || mock.erases != 0 {
		t.Fatal("mismatched backup did not abort before erase")
	}

	mock = &mockSPI{memory: append([]byte(nil), backup...), corruptStage: true}
	if _, err := Install(mock, target, backup, nil); err == nil || mock.memory[0] != 0xff {
		t.Fatal("staged readback failure committed boot header")
	}
}
