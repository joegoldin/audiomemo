package transcribe

import "encoding/binary"

// WAVHeader returns the 44-byte RIFF header for dataLen bytes of 16-bit PCM,
// so audio held in memory can be sent as a WAV without copying the samples.
func WAVHeader(dataLen, sampleRate, channels int) []byte {
	const bitsPerSample = 16
	blockAlign := channels * bitsPerSample / 8
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+dataLen))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(h[20:], 1)  // PCM
	binary.LittleEndian.PutUint16(h[22:], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[28:], uint32(sampleRate*blockAlign))
	binary.LittleEndian.PutUint16(h[32:], uint16(blockAlign))
	binary.LittleEndian.PutUint16(h[34:], bitsPerSample)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(dataLen))
	return h
}
