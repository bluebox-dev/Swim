// Package msi builds a per-site SWIM Agent installer with wixl (msitools).
//
// The MSI installs swim-agent.exe, registers the SWIMAgent service (LocalSystem,
// auto start) and writes one-shot bootstrap values to HKLM\SOFTWARE\SWIM\Bootstrap.
// No custom actions: the agent enrolls itself on first start and deletes the token,
// so an MSI that is installed while offline still enrolls once the network is back.
package msi

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// UpgradeCode never changes: a newer agent MSI upgrades an older one in place.
const UpgradeCode = "6A0D1C1E-1B8E-4B7E-9A51-5A1F0C3E5700"

// ProductCode is fixed per agent version, so running the same version's MSI
// twice is a no-op instead of a second, conflicting product.
func ProductCode(version string) string {
	h := sha1.Sum([]byte(UpgradeCode + "/" + version))
	h[6] = (h[6] & 0x0f) | 0x50 // RFC 4122 v5 layout
	h[8] = (h[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16]))
}

type Params struct {
	Exe     string // path to swim-agent.exe on the server
	Version string // x.y.z
	Server  string // https://…
	Pin     string // sha256 of the server cert
	Token   string // enrollment token (one-shot)
	Site    string // label only
}

var tpl = template.Must(template.New("wxs").Funcs(template.FuncMap{"x": xmlEsc}).Parse(`<?xml version="1.0" encoding="utf-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product Id="{{.ProductCode}}" Name="SWIM Agent" Language="1033" Version="{{x .Version}}" Manufacturer="SWIM"
           UpgradeCode="{{.UpgradeCode}}">
    <Package InstallerVersion="500" Compressed="yes" InstallScope="perMachine"
             Description="SWIM Agent {{x .Version}}{{if .Site}} ({{x .Site}}){{end}}" Comments="Staged Windows Image Manager agent"/>
    <MajorUpgrade DowngradeErrorMessage="A newer SWIM Agent is already installed."/>
    <Media Id="1" Cabinet="agent.cab" EmbedCab="yes"/>
    <Property Id="ARPNOMODIFY" Value="1"/>
    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="ProgramFiles64Folder">
        <Directory Id="INSTALLDIR" Name="SWIM">
          <Component Id="Agent" Guid="6A0D1C1E-1B8E-4B7E-9A51-5A1F0C3E5701" Win64="yes">
            <File Id="AgentExe" Source="{{x .Exe}}" Name="swim-agent.exe" KeyPath="yes"/>
            <ServiceInstall Id="Svc" Name="SWIMAgent" DisplayName="SWIM Agent" Type="ownProcess" Start="auto"
                            ErrorControl="normal" Arguments="run" Account="LocalSystem"
                            Description="Staged Windows Image Manager agent: reports to the SWIM server and runs approved reimage jobs."/>
            <ServiceControl Id="SvcCtl" Name="SWIMAgent" Start="install" Stop="both" Remove="uninstall" Wait="no"/>
            <RegistryValue Root="HKLM" Key="SOFTWARE\SWIM\Bootstrap" Name="Server" Type="string" Value="{{x .Server}}"/>
            <RegistryValue Root="HKLM" Key="SOFTWARE\SWIM\Bootstrap" Name="Pin" Type="string" Value="{{x .Pin}}"/>
            <RegistryValue Root="HKLM" Key="SOFTWARE\SWIM\Bootstrap" Name="Token" Type="string" Value="{{x .Token}}"/>
            <RegistryValue Root="HKLM" Key="SOFTWARE\SWIM\Bootstrap" Name="Site" Type="string" Value="{{x .Site}}"/>
          </Component>
        </Directory>
      </Directory>
    </Directory>
    <Feature Id="Main" Level="1"><ComponentRef Id="Agent"/></Feature>
  </Product>
</Wix>
`))

func xmlEsc(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Build writes the MSI into dir and returns its path.
func Build(ctx context.Context, p Params, dir string) (string, error) {
	// wixl only resolves Source paths relative to the working directory.
	if err := copyFile(p.Exe, filepath.Join(dir, "swim-agent.exe")); err != nil {
		return "", fmt.Errorf("agent exe: %w", err)
	}
	var wxs bytes.Buffer
	if err := tpl.Execute(&wxs, map[string]string{
		"ProductCode": ProductCode(p.Version), "UpgradeCode": UpgradeCode,
		"Exe": "swim-agent.exe", "Version": p.Version, "Server": p.Server, "Pin": p.Pin, "Token": p.Token, "Site": p.Site,
	}); err != nil {
		return "", err
	}
	src := filepath.Join(dir, "agent.wxs")
	out := filepath.Join(dir, "agent.msi")
	if err := os.WriteFile(src, wxs.Bytes(), 0o600); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wixl", "-a", "x64", "-o", out, src)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("wixl: %v: %s", err, b)
	}
	return out, nil
}
