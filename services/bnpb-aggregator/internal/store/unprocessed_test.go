package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestChunkedUnprocessedPayloadRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("oversized-pvmbg-payload"), 900_000)
	chunks := splitUnprocessedPayload("msg-large", payload)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want multiple chunks", len(chunks))
	}
	digest := sha256.Sum256(payload)
	message := &UnprocessedMessage{
		MessageID: "msg-large", RawPayloadChunked: true, RawPayloadSize: len(payload),
		RawPayloadSHA256: hex.EncodeToString(digest[:]), RawPayloadChunks: len(chunks),
	}
	got, err := assembleUnprocessedPayload(message, chunks)
	if err != nil {
		t.Fatalf("assembleUnprocessedPayload() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("assembled payload differs from original")
	}
}

func TestChunkedUnprocessedPayloadRejectsMissingOrCorruptChunk(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), unprocessedPayloadChunkSize+10)
	chunks := splitUnprocessedPayload("msg-large", payload)
	digest := sha256.Sum256(payload)
	message := &UnprocessedMessage{
		MessageID: "msg-large", RawPayloadChunked: true, RawPayloadSize: len(payload),
		RawPayloadSHA256: hex.EncodeToString(digest[:]), RawPayloadChunks: len(chunks),
	}
	if _, err := assembleUnprocessedPayload(message, chunks[:1]); err == nil {
		t.Fatal("missing chunk accepted")
	}
	chunks[1].Data[0] ^= 0xff
	if _, err := assembleUnprocessedPayload(message, chunks); err == nil {
		t.Fatal("corrupt chunk accepted")
	}
}
