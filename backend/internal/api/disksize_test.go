package api

import (
	"strconv"
	"testing"

	"swim/pkg/manifest"
)

func TestDiskTooSmall(t *testing.T) {
	const gb = int64(1) << 30
	img := 57*gb + gb/2 // v2026.11.24 expands to ~57.6 GB → needs 58+15 = 73 GB
	inv := []byte(`{"disks":[{"drive":"C:","size":` + i64s(98*gb) + `}],"physical_disks":[{"size":1048576},{"size":` + i64s(100*gb) + `}]}`)
	cases := []struct {
		name string
		d    manifest.DiskProfile
		inv  []byte
		bad  bool
	}{
		{"60 GB C: too small", manifest.DiskProfile{Mode: "repartition", SystemGB: 60}, inv, true},
		{"80 GB C: fits", manifest.DiskProfile{Mode: "repartition", SystemGB: 80}, inv, false},
		{"all space on 100 GB disk", manifest.DiskProfile{Mode: "repartition"}, inv, false},
		{"refresh on 98 GB C:", manifest.DiskProfile{Mode: "refresh"}, inv, false},
		{"refresh on 50 GB C:", manifest.DiskProfile{Mode: "refresh"}, []byte(`{"disks":[{"drive":"C:","size":` + i64s(50*gb) + `}]}`), true},
		{"small disk", manifest.DiskProfile{Mode: "repartition"}, []byte(`{"physical_disks":[{"size":` + i64s(80*gb) + `}]}`), true},
		{"no inventory", manifest.DiskProfile{Mode: "refresh"}, nil, false},
	}
	for _, c := range cases {
		if got := diskTooSmall(c.d, img, c.inv); (got != "") != c.bad {
			t.Errorf("%s: got %q", c.name, got)
		}
	}
}

func i64s(n int64) string { return strconv.FormatInt(n, 10) }
