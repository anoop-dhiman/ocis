package blobstore

// chunkSize = 1024 * 1024 // 1MB chunks
// Encrypted file format:
// [12 bytes: Nonce]
// [4 bytes: Chunk 1 Size][Chunk 1 Data + 16 bytes GCM tag]
// [4 bytes: Chunk 2 Size][Chunk 2 Data + 16 bytes GCM tag]
// [4 bytes: Chunk 3 Size][Chunk 3 Data + 16 bytes GCM tag]
// ...
// [4 bytes: Last Chunk Size][Last Chunk Data + 16 bytes GCM tag]
func getChunkNumber(position int64) int {
	// Since position is in decrypted file, we can directly calculate chunk number
	// by dividing by the chunk size (1MB)
	return int(position / DefaultChunkSize)
}

// getEncryptedChunkPosition returns the start position in encrypted file for a given chunk number
func getEncryptedChunkPosition(chunkNum int) int64 {
	// Start with nonce size
	pos := int64(12)

	// For each chunk before the target chunk:
	// - 4 bytes for chunk size
	// - 1MB + 16 bytes for encrypted data
	for i := 0; i < chunkNum; i++ {
		pos += 4 + (DefaultChunkSize + 16)
	}

	return pos
}

// Helper function to find minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
