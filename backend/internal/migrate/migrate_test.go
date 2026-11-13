package migrate

import (
	"testing"
	"testing/fstest"
)

func TestLoad(t *testing.T) {
	file := func(sql string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(sql)} }
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		wantErr bool
		want    []string
	}{
		{name: "ordered by number", fsys: fstest.MapFS{"0002_b.sql": file("b"), "0001_a.sql": file("a")}, want: []string{"a", "b"}},
		{name: "empty directory", fsys: fstest.MapFS{}, want: nil},
		{name: "gap", fsys: fstest.MapFS{"0001_a.sql": file("a"), "0003_c.sql": file("c")}, wantErr: true},
		{name: "starts at two", fsys: fstest.MapFS{"0002_b.sql": file("b")}, wantErr: true},
		{name: "duplicate version", fsys: fstest.MapFS{"0001_a.sql": file("a"), "0001_b.sql": file("b")}, wantErr: true},
		{name: "bad name", fsys: fstest.MapFS{"init.sql": file("a")}, wantErr: true},
		{name: "upper case name", fsys: fstest.MapFS{"0001_Init.sql": file("a")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(tt.fsys)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load error = %v, wantErr %v", err, tt.wantErr)
			}
			var names []string
			for _, m := range got {
				names = append(names, m.Name)
			}
			if len(names) != len(tt.want) {
				t.Fatalf("Load = %v, want %v", names, tt.want)
			}
			for i := range names {
				if names[i] != tt.want[i] {
					t.Fatalf("Load = %v, want %v", names, tt.want)
				}
			}
		})
	}
}

func TestLoad_checksumChangesWithContent(t *testing.T) {
	a, _ := Load(fstest.MapFS{"0001_a.sql": {Data: []byte("select 1")}})
	b, _ := Load(fstest.MapFS{"0001_a.sql": {Data: []byte("select 2")}})
	if a[0].Checksum == b[0].Checksum {
		t.Fatal("different SQL must give different checksums")
	}
}
