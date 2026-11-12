package backends

import (
	"io"
	"testing"

	minio "github.com/minio/minio-go/v7"
)

// An FTP transfer has no length until its last byte arrives, so every upload is
// planned as an unknown-size one. minio-go plans that as if the object could be
// as large as the layout allows, which parts at about 537MiB - see the comment
// on uploadPartSize - so one scanner page of 0.3MB allocated 537MiB of live
// heap. The part size of uploadOptions is what stops that, and this is the only
// place the number is checked.
func TestUploadOptionsBoundThePartSize(t *testing.T) {
	_, partSize, _, err := minio.OptimalPartInfo(-1, uploadOptions().PartSize)
	if err != nil {
		t.Fatalf("OptimalPartInfo with the part size of the backend: %v", err)
	}
	if limit := int64(32 << 20); partSize > limit {
		t.Errorf("a streaming upload buffers %d bytes per part, want at most %d", partSize, limit)
	}

	_, unplanned, _, err := minio.OptimalPartInfo(-1, 0)
	if err != nil {
		t.Fatalf("OptimalPartInfo with no part size: %v", err)
	}
	if floor := int64(100 << 20); unplanned < floor {
		t.Errorf("an unplanned upload parts at %d bytes; this test only means something while the library plans one for the largest object", unplanned)
	}
}

// minioStorage.Close and webdavStorage.Close ask their backend through
// io.Closer, so the concrete backends have to keep answering it.
func TestBackendsAreClosable(t *testing.T) {
	var minioBackend io.Closer = &minioBackendImpl{}
	var webDavBackend io.Closer = &webDavBackendImpl{}

	// A backend with no transport of its own has nothing to release, and closing
	// it still has to succeed.
	if err := minioBackend.Close(); err != nil {
		t.Errorf("minioBackendImpl.Close with no transport: %v", err)
	}
	if err := webDavBackend.Close(); err != nil {
		t.Errorf("webDavBackendImpl.Close with no transport: %v", err)
	}
}
