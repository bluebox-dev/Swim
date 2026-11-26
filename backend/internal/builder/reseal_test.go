package builder

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"swim/backend/internal/vm"
)

const gmXML = `<domain type='kvm'>
  <name>gm-win11</name>
  <on_reboot>restart</on_reboot>
  <uuid>144bc6eb-736b-4515-9609-a1c887195ae8</uuid>
  <os firmware='efi'>
    <nvram template='/usr/share/OVMF/OVMF_VARS_4M.ms.fd'>/var/lib/libvirt/qemu/nvram/gm-win11_VARS.fd</nvram>
    <boot dev='cdrom'/>
    <boot dev='hd'/>
  </os>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='/srv/swim/vms/gm-win11.qcow2'/>
      <target dev='sda' bus='sata'/>
    </disk>
    <disk type='file' device='cdrom'>
      <source file='/srv/swim/vms/iso/Windows11.iso'/>
      <target dev='sdb' bus='sata'/>
    </disk>
    <interface type='network'>
      <mac address='52:54:00:11:22:33'/>
      <source network='default'/>
    </interface>
    <tpm model='tpm-crb'><backend type='emulator' version='2.0'/></tpm>
    <graphics type='vnc' port='5901' autoport='no' listen='127.0.0.1'/>
    <input type='tablet' bus='usb'><address type='usb' bus='0' port='1'/></input>
  </devices>
</domain>`

func TestResealDomainXML(t *testing.T) {
	x, err := resealDomainXML(gmXML, "swim-reseal-x", "/srv/swim/vms/capture-x.qcow2", "/srv/swim/vms/gm-win11.qcow2")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<uuid>", "cdrom", "<interface", "gm-win11.qcow2", "gm-win11_VARS", "port='5901'", "Windows11.iso"} {
		if strings.Contains(x, bad) {
			t.Errorf("still contains %q:\n%s", bad, x)
		}
	}
	for _, want := range []string{"<name>swim-reseal-x</name>", "<source file='/srv/swim/vms/capture-x.qcow2'/>",
		"/var/lib/libvirt/qemu/nvram/swim-reseal-x_VARS.fd</nvram>", "bus='sata'", "<tpm", "<boot dev='hd'/>", "port='1'/>", "<on_reboot>destroy</on_reboot>", "<graphics port='-1' autoport='yes' type='vnc'"} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %q:\n%s", want, x)
		}
	}
}

// Live test on the lab server (inside the builder container):
//
//	SWIM_RESEAL_GM=gm-win11 SWIM_RESEAL_DISK=/srv/swim/vms/gm-win11.qcow2 SWIM_RESEAL_OVERLAY=/srv/swim/vms/reseal-test.qcow2 ./builder.test -test.run TestResealLive -test.v
func TestResealLive(t *testing.T) {
	gm, disk, overlay := os.Getenv("SWIM_RESEAL_GM"), os.Getenv("SWIM_RESEAL_DISK"), os.Getenv("SWIM_RESEAL_OVERLAY")
	if gm == "" || disk == "" || overlay == "" {
		t.Skip("lab-only: set SWIM_RESEAL_GM, SWIM_RESEAL_DISK, SWIM_RESEAL_OVERLAY")
	}
	if b, err := exec.Command("qemu-img", "create", "-q", "-f", "qcow2", "-b", disk, "-F", "qcow2", overlay).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	w := &Worker{LV: vm.Libvirt{URI: "qemu:///system"}}
	logf := func(f string, a ...any) { t.Logf(f, a...) }
	if err := w.reseal(context.Background(), "img_resealtest", gm, disk, overlay, logf); err != nil {
		t.Fatal(err)
	}
	_ = withNBD(context.Background(), overlay, logf, func(part string) error {
		res, err := imageChecks(part)
		for _, r := range res {
			t.Logf("%s passed=%v %s — %s", r.ID, r.Passed, r.Name, r.Detail)
		}
		return err
	})
}
