package firmware

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	ROMSize    = 15400
	BaseSHA256 = "fc162ef47d0b62ede04221d4d0b2f3486935f7a1a609006a96b3e560d63def88"
	V3SHA256   = "af457547495d40ad1e42647a7d76a7f0bd2b757f3b65a1f082876821c5b3d75c"
)

func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fromHex(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return decoded
}

func unique(haystack, needle []byte) (int, error) {
	first := bytes.Index(haystack, needle)
	if first < 0 || bytes.Index(haystack[first+1:], needle) >= 0 {
		return 0, fmt.Errorf("signature missing or repeated: %x", needle)
	}
	return first, nil
}

func descriptors(data []byte) ([][]byte, error) {
	var result [][]byte
	for at := 0; at < len(data); {
		length := int(data[at])
		if length < 2 || at+length > len(data) {
			return nil, fmt.Errorf("invalid descriptor at 0x%x", at)
		}
		result = append(result, data[at:at+length])
		at += length
	}
	return result, nil
}

func checksum(data []byte) uint16 {
	var sum uint32
	for _, value := range data[0x10:] {
		sum += uint32(value)
	}
	return uint16(sum)
}

// PatchV3 accepts only the exact official ES9260 3.6.14 external ROM.
// The resulting image is also pinned to the byte-for-byte validated v3 hash.
func PatchV3(base []byte) ([]byte, error) {
	if len(base) != ROMSize || Hash(base) != BaseSHA256 {
		return nil, errors.New("official ES9260 3.6.14 base ROM hash or size mismatch")
	}
	if base[0] != 0x45 || base[1] != 0x04 || binary.LittleEndian.Uint16(base[2:4]) != checksum(base) {
		return nil, errors.New("base ROM header or checksum mismatch")
	}

	activeSignature := fromHex("09025501040100a000090400000001010000")
	inactiveSignature := fromHex("0902ba00030100a000090400000001010000")
	activeAt, err := unique(base, activeSignature)
	if err != nil {
		return nil, err
	}
	inactiveAt, err := unique(base, inactiveSignature)
	if err != nil {
		return nil, err
	}
	if activeAt+int(binary.LittleEndian.Uint16(base[activeAt+2:activeAt+4])) != inactiveAt {
		return nil, errors.New("configuration layout mismatch")
	}
	inactiveEnd := inactiveAt + int(binary.LittleEndian.Uint16(base[inactiveAt+2:inactiveAt+4]))
	if inactiveEnd > len(base) {
		return nil, errors.New("inactive configuration exceeds ROM")
	}

	active := append([]byte(nil), base[activeAt:inactiveAt]...)
	fu := fromHex("0a240602010101020200")
	oldMixer := fromHex("0d240408020701020300000000")
	newMixer := fromHex("0d240408020702020300000000")
	mixerAt, err := unique(active, oldMixer)
	if err != nil {
		return nil, err
	}
	active = append(active[:mixerAt], append(fu, active[mixerAt:]...)...)
	copy(active[mixerAt+len(fu):], newMixer)
	binary.LittleEndian.PutUint16(active[2:4], uint16(len(active)))
	acAt, err := unique(active, fromHex("0a240100016000020102"))
	if err != nil {
		return nil, err
	}
	binary.LittleEndian.PutUint16(active[acAt+5:acAt+7], 0x006a)
	if len(active) != 0x015f {
		return nil, errors.New("active configuration length mismatch")
	}
	if _, err := descriptors(active); err != nil {
		return nil, err
	}

	inactive := base[inactiveAt:inactiveEnd]
	parts, err := descriptors(inactive)
	if err != nil {
		return nil, err
	}
	formats := map[string][]byte{
		"142402010202100480bb0000770144ac00885801": fromHex("0e2402010202100280bb00007701"),
		"142402010203180480bb0000770144ac00885801": fromHex("0e2402010203180280bb00007701"),
	}
	compact := make([]byte, 0, len(inactive)-len(fu))
	for _, part := range parts {
		if replacement, found := formats[hex.EncodeToString(part)]; found {
			compact = append(compact, replacement...)
			delete(formats, hex.EncodeToString(part))
		} else {
			compact = append(compact, part...)
		}
	}
	if len(formats) != 0 {
		return nil, errors.New("inactive sample-rate descriptors mismatch")
	}
	compact = append(compact, 0x02, 0xff)
	if len(compact) != 0x00b0 {
		return nil, errors.New("inactive configuration length mismatch")
	}
	binary.LittleEndian.PutUint16(compact[2:4], uint16(len(compact)))
	if _, err := descriptors(compact); err != nil {
		return nil, err
	}

	result := make([]byte, 0, len(base))
	result = append(result, base[:activeAt]...)
	result = append(result, active...)
	result = append(result, compact...)
	result = append(result, base[inactiveEnd:]...)
	if len(result) != ROMSize || activeAt+len(active)+len(compact) != inactiveEnd {
		return nil, errors.New("ROM length or post-descriptor offset moved")
	}
	binary.LittleEndian.PutUint16(result[2:4], checksum(result))
	if Hash(result) != V3SHA256 {
		return nil, errors.New("produced v3 does not match the validated ROM")
	}
	return result, nil
}
