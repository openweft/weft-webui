package wclient

import (
	"testing"

	weftv1 "github.com/openweft/weft-proto"
)

// The S3 secret is write-only: even if a server filled the deprecated
// BucketInfo.secret_access_key, no row built for the API may carry it.
func TestBucketRowNeverCarriesTheSecret(t *testing.T) {
	row := bucketRow(&weftv1.BucketInfo{
		Uuid:            "u1",
		Name:            "data",
		AccessKeyId:     "AKIATEST",
		SecretAccessKey: "s3cr3t-never-returned",
	})
	if _, ok := row["secret_access_key"]; ok {
		t.Error("bucketRow carries secret_access_key")
	}
	for k, v := range row {
		if s, ok := v.(string); ok && s == "s3cr3t-never-returned" {
			t.Errorf("bucketRow[%q] leaks the secret", k)
		}
	}
	if row["access_key_id"] != "AKIATEST" || row["name"] != "data" {
		t.Errorf("bucketRow lost its other fields: %v", row)
	}
	if got := bucketRow(nil); len(got) != 0 {
		t.Errorf("bucketRow(nil) = %v, want empty", got)
	}
}
