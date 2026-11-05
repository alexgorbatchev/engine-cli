package discovery

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsMusicFolderBalancesCOM(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	want, err := windows.KnownFolderPath(windows.FOLDERID_Music, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := musicFolder(); err != nil || got != want || !filepath.IsAbs(got) {
		t.Fatalf("Music folder = %q, %v; want %q", got, err, want)
	}

	// A fresh S_OK proves the first lookup released its COM initialization.
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil {
		if errors.Is(err, syscall.Errno(windows.S_FALSE)) {
			windows.CoUninitialize()
		}
		t.Fatalf("COM initialization after lookup: %v", err)
	}
	func() {
		defer windows.CoUninitialize()
		for range 2 {
			if got, err := musicFolder(); err != nil || got != want {
				t.Fatalf("Music lookup with initialized COM = %q, %v; want %q", got, err, want)
			}
		}
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); !errors.Is(err, syscall.Errno(windows.RPC_E_CHANGED_MODE)) {
			if err == nil || errors.Is(err, syscall.Errno(windows.S_FALSE)) {
				windows.CoUninitialize()
			}
			t.Fatalf("Music lookup changed the caller's COM apartment: %v", err)
		}
	}()

	// Switching apartments only succeeds if both nested S_FALSE calls were
	// balanced and the caller's single initialization was left intact.
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		if errors.Is(err, syscall.Errno(windows.S_FALSE)) {
			windows.CoUninitialize()
		}
		t.Fatalf("COM apartment change after lookups: %v", err)
	}
	defer windows.CoUninitialize()
	if got, err := musicFolder(); got != "" || !errors.Is(err, syscall.Errno(windows.RPC_E_CHANGED_MODE)) {
		t.Fatalf("Music lookup in incompatible apartment = %q, %v", got, err)
	}
}

func TestWindowsDefaultLocationsDiscoverProfileLibrary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		apartment uint32
	}{
		{"known Music folder", windows.COINIT_APARTMENTTHREADED},
		{"Music lookup failure", windows.COINIT_MULTITHREADED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("USERPROFILE", home)
			library := libraryFixture(t, filepath.Join(home, "Music", libraryName), false)
			canonical, err := filepath.EvalSymlinks(library)
			if err != nil {
				t.Fatal(err)
			}
			database := filepath.Join(canonical, "Database2", "m.db")
			before, err := os.ReadFile(database)
			if err != nil {
				t.Fatal(err)
			}

			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			if err := windows.CoInitializeEx(0, tc.apartment); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
				t.Fatal(err)
			}
			defer windows.CoUninitialize()

			roots, warnings := defaultLocations()
			localCount := 1
			if tc.apartment == windows.COINIT_APARTMENTTHREADED {
				music, err := windows.KnownFolderPath(windows.FOLDERID_Music, windows.KF_FLAG_DONT_VERIFY)
				if err != nil {
					t.Fatal(err)
				}
				want := location{Path: music, Name: "Local library", Source: "local"}
				if len(warnings) != 0 || len(roots) < 2 || roots[0] != want {
					t.Fatalf("known Music folder must precede fallback: roots=%+v warnings=%+v", roots, warnings)
				}
				localCount++
			} else if len(warnings) != 1 || !strings.HasPrefix(warnings[0].Message, "locating Music folder: ") {
				t.Fatalf("Music failure warning = %+v", warnings)
			}
			want := location{Path: filepath.Join(home, "Music"), Name: "Local library", Source: "local"}
			if len(roots) < localCount || roots[localCount-1] != want {
				t.Fatalf("profile fallback missing: %+v", roots)
			}
			assertWindowsDriveLocations(t, roots[localCount:])

			report, err := Discover(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			matches := 0
			for _, found := range report.Libraries {
				if found.Path != canonical {
					continue
				}
				matches++
				if found.Name != "Local library" || found.Source != "local" || found.Registration != "unregistered" || found.Database != database {
					t.Fatalf("profile library = %+v", found)
				}
			}
			if matches != 1 {
				t.Fatalf("profile library matched %d times: %+v", matches, report)
			}
			for _, warning := range warnings {
				if !slices.Contains(report.Warnings, warning) {
					t.Fatalf("discovery lost native warning %+v: %+v", warning, report.Warnings)
				}
			}
			if _, err := os.Stat(filepath.Join(library, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("discovery wrote library state: %v", err)
			}
			after, err := os.ReadFile(database)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("discovery changed library database: %v", err)
			}
		})
	}
}

func assertWindowsDriveLocations(t *testing.T, roots []location) {
	t.Helper()
	// Independently enumerate root strings instead of reproducing the
	// production bitmask-to-drive-letter conversion.
	n, err := windows.GetLogicalDriveStrings(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, n)
	n, err = windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil || int(n) >= len(buf) {
		t.Fatalf("reading native drive roots: length=%d capacity=%d err=%v", n, len(buf), err)
	}
	want := map[string]bool{}
	for len(buf) > 0 && buf[0] != 0 {
		end := slices.Index(buf, uint16(0))
		if end < 0 {
			t.Fatal("unterminated native drive root")
		}
		path := windows.UTF16ToString(buf[:end])
		kind := windows.GetDriveType(&buf[0])
		buf = buf[end+1:]
		if kind == windows.DRIVE_FIXED || kind == windows.DRIVE_REMOVABLE {
			want[strings.ToUpper(filepath.Clean(path))] = true
		}
	}
	if len(want) == 0 || len(roots) != len(want) {
		t.Fatalf("discovered drives=%+v; native fixed/removable roots=%v", roots, want)
	}
	for _, root := range roots {
		key := strings.ToUpper(root.Path)
		if !want[key] || root.Source != "drive" {
			t.Fatalf("unexpected or duplicate drive: %+v", root)
		}
		delete(want, key)
		ptr, err := windows.UTF16PtrFromString(root.Path)
		if err != nil {
			t.Fatal(err)
		}
		name := "Drive: " + filepath.VolumeName(root.Path)
		var label [windows.MAX_PATH + 1]uint16
		if err := windows.GetVolumeInformation(ptr, &label[0], uint32(len(label)), nil, nil, nil, nil, 0); err == nil {
			if text := windows.UTF16ToString(label[:]); text != "" {
				name = "Drive: " + text + " (" + filepath.VolumeName(root.Path) + ")"
			}
		}
		if root.Name != name {
			t.Fatalf("drive label = %q; want %q", root.Name, name)
		}
	}
}
