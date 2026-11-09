package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/authorization"
	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/device"
	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/firmware"
	"github.com/Xboxpig/asus-mu3c-ad-m2-es9260/internal/flash"
)

const v2SHA256 = "6026936d5c3dd3f3e96838c9b0fc28d1aa0ca794f2f7e0817e0d36f2ce41119c"

type session struct {
	Challenge  authorization.Challenge `json:"challenge"`
	DevicePath string                  `json:"device_path"`
	BaseSHA256 string                  `json:"base_sha256"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "prepare":
		err = prepare(os.Args[2:])
	case "install":
		err = install(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "MU3C-AD(M2) v3 Windows firmware installer")
	fmt.Fprintln(os.Stderr, "  mu3c-installer.exe prepare -base ES9260_3.6.14_EXT.rom -out SESSION_DIR [-device HID_PATH]")
	fmt.Fprintln(os.Stderr, "  mu3c-installer.exe install -base ES9260_3.6.14_EXT.rom -session SESSION_DIR")
	fmt.Fprintln(os.Stderr, "  mu3c-installer.exe verify -session SESSION_DIR")
}

func loadTarget(path string) ([]byte, error) {
	base, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return firmware.PatchV3(base)
}

func randomAttemptID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func writeNewSync(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func encodeJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func knownCurrentROM(eeprom []byte) bool {
	if len(eeprom) != flash.EEPROMSize {
		return false
	}
	current := firmware.Hash(eeprom[:firmware.ROMSize])
	return current == firmware.BaseSHA256 || current == v2SHA256 || current == firmware.V3SHA256
}

func prepare(arguments []string) error {
	options := flag.NewFlagSet("prepare", flag.ContinueOnError)
	basePath := options.String("base", "", "official ASUS ES9260 3.6.14 ROM")
	outDir := options.String("out", "", "new directory for this installation attempt")
	devicePath := options.String("device", "", "optional exact Windows HID path")
	if err := options.Parse(arguments); err != nil {
		return err
	}
	if *basePath == "" || *outDir == "" || options.NArg() != 0 {
		return errors.New("prepare requires -base and -out")
	}
	target, err := loadTarget(*basePath)
	if err != nil {
		return err
	}
	attemptID, err := randomAttemptID()
	if err != nil {
		return err
	}
	deviceHandle, err := device.Open(*devicePath)
	if err != nil {
		return err
	}
	defer deviceHandle.Close()
	flashType, err := flash.FlashType(deviceHandle)
	if err != nil || flashType != 1 {
		return fmt.Errorf("expected SPI flash type 1, got %d: %v", flashType, err)
	}
	fmt.Println("Reading complete 32 KiB EEPROM twice from", deviceHandle.Path)
	first, err := flash.ReadEEPROM(deviceHandle)
	if err != nil {
		return err
	}
	second, err := flash.ReadEEPROM(deviceHandle)
	if err != nil {
		return err
	}
	if !bytes.Equal(first, second) {
		return errors.New("two full EEPROM reads differ; no session was created")
	}
	if !knownCurrentROM(first) {
		return fmt.Errorf("current 15,400-byte ROM hash %s is not a verified base/v2/v3 image", firmware.Hash(first[:firmware.ROMSize]))
	}
	challenge := authorization.Challenge{
		Purpose:      authorization.Purpose,
		AttemptID:    attemptID,
		DeviceID:     firmware.Hash([]byte(strings.ToLower(deviceHandle.Path))),
		BackupSHA256: firmware.Hash(first),
		TargetSHA256: firmware.Hash(target),
		CreatedUTC:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := authorization.ValidateChallenge(challenge, time.Now()); err != nil {
		return err
	}
	state := session{Challenge: challenge, DevicePath: deviceHandle.Path, BaseSHA256: firmware.BaseSHA256}
	stateJSON, err := encodeJSON(state)
	if err != nil {
		return err
	}
	challengeJSON, err := encodeJSON(challenge)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*outDir), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(*outDir, 0700); err != nil {
		return fmt.Errorf("session directory must not already exist: %w", err)
	}
	files := []struct {
		name string
		data []byte
	}{
		{"backup-1.bin", first},
		{"backup-2.bin", second},
		{"challenge.json", challengeJSON},
		{"session.json", stateJSON},
	}
	for _, file := range files {
		if err := writeNewSync(filepath.Join(*outDir, file.name), file.data); err != nil {
			return fmt.Errorf("persist %s: %w; keep any completed backups", file.name, err)
		}
	}
	for _, file := range files[:2] {
		disk, err := os.ReadFile(filepath.Join(*outDir, file.name))
		if err != nil || !bytes.Equal(disk, first) {
			return fmt.Errorf("backup disk verification failed for %s: %v", file.name, err)
		}
	}
	fmt.Println("Backups saved and verified:", filepath.Join(*outDir, "backup-1.bin"), "and", filepath.Join(*outDir, "backup-2.bin"))
	fmt.Println("Backup SHA-256:", challenge.BackupSHA256)
	fmt.Println("Installation session prepared successfully.")
	fmt.Println("Run: mu3c-installer.exe install -base", *basePath, "-session", *outDir)
	return nil
}

func install(arguments []string) error {
	options := flag.NewFlagSet("install", flag.ContinueOnError)
	basePath := options.String("base", "", "official ASUS ES9260 3.6.14 ROM")
	sessionDir := options.String("session", "", "directory created by prepare")
	if err := options.Parse(arguments); err != nil {
		return err
	}
	if *basePath == "" || *sessionDir == "" || options.NArg() != 0 {
	    return errors.New("install requires -base and -session")
	}
	if _, err := os.Stat(filepath.Join(*sessionDir, "install-started.json")); err == nil {
		return errors.New("this installation session was already used; prepare and back up again")
	} else if !os.IsNotExist(err) {
		return err
	}
	target, err := loadTarget(*basePath)
	if err != nil {
		return err
	}
	stateBytes, err := os.ReadFile(filepath.Join(*sessionDir, "session.json"))
	if err != nil {
		return err
	}
	var state session
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		return err
	}
	if state.BaseSHA256 != firmware.BaseSHA256 || state.Challenge.TargetSHA256 != firmware.Hash(target) || state.DevicePath == "" {
		return errors.New("session base, target or HID identity mismatch")
	}
	challengeBytes, err := os.ReadFile(filepath.Join(*sessionDir, "challenge.json"))
	if err != nil {
		return err
	}
	challenge, err := authorization.DecodeChallenge(challengeBytes)
	if err != nil || challenge != state.Challenge {
		return errors.New("challenge.json differs from session.json")
	}
	first, err := os.ReadFile(filepath.Join(*sessionDir, "backup-1.bin"))
	if err != nil {
		return err
	}
	second, err := os.ReadFile(filepath.Join(*sessionDir, "backup-2.bin"))
	if err != nil {
		return err
	}
	if len(first) != flash.EEPROMSize || !bytes.Equal(first, second) || firmware.Hash(first) != challenge.BackupSHA256 {
		return errors.New("two persisted EEPROM backups differ, are incomplete or fail the challenge hash")
	}
	deviceHandle, err := device.Open(state.DevicePath)
	if err != nil {
		return err
	}
	defer deviceHandle.Close()
	if firmware.Hash([]byte(strings.ToLower(deviceHandle.Path))) != challenge.DeviceID {
		return errors.New("HID identity differs from installation session")
	}
	fresh, err := flash.ReadEEPROM(deviceHandle)
	if err != nil {
		return err
	}
	if !bytes.Equal(fresh, first) {
		return errors.New("current EEPROM differs from backed-up session; run prepare again")
	}
	if err := writeNewSync(filepath.Join(*sessionDir, "pre-install.bin"), fresh); err != nil {
		return fmt.Errorf("persist fresh pre-install backup: %w", err)
	}
	verifiedFresh, err := os.ReadFile(filepath.Join(*sessionDir, "pre-install.bin"))
	if err != nil || !bytes.Equal(verifiedFresh, fresh) {
		return fmt.Errorf("fresh pre-install backup verification failed: %v", err)
	}
	startReceipt, err := encodeJSON(map[string]string{
		"attempt_id":    challenge.AttemptID,
		"backup_sha256": challenge.BackupSHA256,
		"started_utc":   time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if err := writeNewSync(filepath.Join(*sessionDir, "install-started.json"), startReceipt); err != nil {
		return fmt.Errorf("persist install attempt marker: %w", err)
	}
	fmt.Println("Local installation session accepted for attempt", challenge.AttemptID)
	fmt.Println("Fresh pre-install EEPROM backup persisted; checking live EEPROM again before erase")
	readback, err := flash.Install(deviceHandle, target, first, func(message string) { fmt.Println(message) })
	if err != nil {
		return err
	}
	if err := writeNewSync(filepath.Join(*sessionDir, "post-flash.bin"), readback); err != nil {
		return fmt.Errorf("firmware verified on device, but saving post-flash readback failed: %w", err)
	}
	completion, err := encodeJSON(map[string]string{
		"attempt_id":    challenge.AttemptID,
		"target_sha256": challenge.TargetSHA256,
		"eeprom_sha256": firmware.Hash(readback),
		"completed_utc": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if err := writeNewSync(filepath.Join(*sessionDir, "install-complete.json"), completion); err != nil {
		return fmt.Errorf("firmware verified on device, but saving completion receipt failed: %w", err)
	}
	fmt.Println("Disconnect the DAC physically, reconnect it, and independently read back the EEPROM.")
	return nil
}

func verify(arguments []string) error {
	options := flag.NewFlagSet("verify", flag.ContinueOnError)
	sessionDir := options.String("session", "", "completed installation session directory")
	if err := options.Parse(arguments); err != nil {
		return err
	}
	if *sessionDir == "" || options.NArg() != 0 {
		return errors.New("verify requires -session")
	}
	if _, err := os.Stat(filepath.Join(*sessionDir, "install-complete.json")); err != nil {
		return fmt.Errorf("installation completion receipt missing: %w", err)
	}
	expected, err := os.ReadFile(filepath.Join(*sessionDir, "post-flash.bin"))
	if err != nil || len(expected) != flash.EEPROMSize {
		return errors.New("verified post-flash 32 KiB image missing")
	}
	deviceHandle, err := device.Open("")
	if err != nil {
		return err
	}
	defer deviceHandle.Close()
	live, err := flash.ReadEEPROM(deviceHandle)
	if err != nil {
		return err
	}
	if !bytes.Equal(live, expected) {
		return errors.New("post-replug EEPROM differs from verified post-flash image")
	}
	fmt.Println("Post-replug full 32 KiB EEPROM matches the flashed image.")
	return nil
}
