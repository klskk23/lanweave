package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDebPackage builds the real server .deb with nfpm and inspects it with dpkg-deb,
// asserting the install layout, the systemd unit's least-privilege/restart/journal fields,
// the maintainer scripts (including the M1 no-echo-password and M2 openssl-dependency
// requirements), and a secret-free example config. It is host-gated: it skips when the
// packaging tools are unavailable.
func TestDebPackage(t *testing.T) {
	for _, tool := range []string{"make", "nfpm", "dpkg-deb"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available — skipping packaging test", tool)
		}
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	// Build the package.
	if out, err := exec.Command("make", "-C", repo, "deb").CombinedOutput(); err != nil {
		t.Fatalf("make deb: %v\n%s", err, out)
	}
	debs, _ := filepath.Glob(filepath.Join(repo, "dist", "lanweave_*_amd64.deb"))
	if len(debs) == 0 {
		t.Fatal("no .deb produced in dist/")
	}
	deb := debs[len(debs)-1]

	// Contents (dpkg-deb -c): the documented files at their paths.
	contents := run(t, "dpkg-deb", "-c", deb)
	for _, p := range []string{"/usr/bin/lanweaved", "/etc/lanweave/config.toml.example", "/lib/systemd/system/lanweaved.service"} {
		if !strings.Contains(contents, p) {
			t.Errorf("package is missing %s", p)
		}
	}

	// Control + Depends (M2): openssl must be a dependency.
	control := run(t, "dpkg-deb", "-f", deb, "Depends")
	if !strings.Contains(control, "openssl") {
		t.Error("package Depends must include openssl (M2)")
	}

	// Maintainer scripts: present + executable.
	ctrlDir := t.TempDir()
	run(t, "dpkg-deb", "-e", deb, ctrlDir)
	for _, s := range []string{"postinst", "prerm", "postrm"} {
		fi, err := os.Stat(filepath.Join(ctrlDir, s))
		if err != nil {
			t.Errorf("missing maintainer script %s: %v", s, err)
			continue
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("maintainer script %s is not executable", s)
		}
	}

	postinst := readFile(t, filepath.Join(ctrlDir, "postinst"))
	// M1: the admin password goes to a root-only file, never echoed.
	if !strings.Contains(postinst, "initial-admin-password") {
		t.Error("postinst must write the admin password to /etc/lanweave/initial-admin-password (M1)")
	}
	if strings.Contains(postinst, "echo \"$ADMIN_PW\"") || strings.Contains(postinst, "echo $ADMIN_PW") {
		t.Error("postinst must not echo the admin password to stdout/logs (M1)")
	}
	// It generates the cert via openssl, enables the service, and references the paths.
	for _, want := range []string{"openssl", "/var/lib/lanweave", "/etc/lanweave", "config.toml", "systemctl"} {
		if !strings.Contains(postinst, want) {
			t.Errorf("postinst should reference %q", want)
		}
	}

	// prerm stops/disables the service (T009/US3).
	prerm := readFile(t, filepath.Join(ctrlDir, "prerm"))
	if !strings.Contains(prerm, "disable") || !strings.Contains(prerm, "lanweaved") {
		t.Error("prerm must stop/disable lanweaved")
	}
	// postrm removes data + config only on purge (T009/US3).
	postrm := readFile(t, filepath.Join(ctrlDir, "postrm"))
	if !strings.Contains(postrm, "purge") || !strings.Contains(postrm, "/var/lib/lanweave") || !strings.Contains(postrm, "/etc/lanweave") {
		t.Error("postrm must remove /var/lib/lanweave and /etc/lanweave on purge")
	}

	// The unit file's required directives (extract the rootfs).
	rootfs := t.TempDir()
	run(t, "dpkg-deb", "-x", deb, rootfs)
	unit := readFile(t, filepath.Join(rootfs, "lib/systemd/system/lanweaved.service"))
	for _, want := range []string{
		"User=root",
		"CapabilityBoundingSet=CAP_NET_ADMIN",
		"Restart=on-failure",
		"StandardOutput=journal",
		"-config /etc/lanweave/config.toml",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit file missing %q", want)
		}
	}

	// The example config ships and carries only placeholders (no real secret).
	example := readFile(t, filepath.Join(rootfs, "etc/lanweave/config.toml.example"))
	if !strings.Contains(example, "CHANGE-ME-ON-FIRST-LOGIN") || !strings.Contains(example, "REPLACE_WITH_A_32_BYTE_RANDOM_SECRET") {
		t.Error("config.toml.example must contain placeholders, not real secrets")
	}
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
