package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/go-enry/go-license-detector/v4/licensedb"
	"github.com/go-enry/go-license-detector/v4/licensedb/filer"
	"github.com/mitchellh/golicense/module"
	"github.com/rsc/goversion/version"
)

type goLicenseConfig struct {
	Allow    []string          `json:"allow"`
	Override map[string]string `json:"override"`
}

func main() {
	log.SetFlags(0)
	config, err := getGoLicenseConfig("golicense.json")
	if err != nil {
		log.Print(err.Error())
		os.Exit(1)
	}
	licenseReport, err := NewLicenseReport(config)
	if err != nil {
		log.Print(err.Error())
		os.Exit(1)
	}
	licenses, err := licenseReport.Run()
	if err != nil {
		log.Print(err.Error())
		os.Exit(1)
	}
	notices := renderNotice(licenses)
	f, err := os.Create("NOTICE.md")
	if err != nil {
		log.Printf("failed to create NOTICE.md (%v)", err)
		os.Exit(1)
	}
	if _, err := f.Write(notices); err != nil {
		_ = f.Close()
		log.Printf("failed to write to NOTICE.md (%v)", err)
		os.Exit(1)
	}
	_ = f.Close()
}

func getGoLicenseConfig(file string) (*goLicenseConfig, error) {
	goLicenseFile, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s (%w)", file, err)
	}
	defer func() {
		_ = goLicenseFile.Close()
	}()
	configData, err := ioutil.ReadAll(goLicenseFile)
	if err != nil {
		log.Fatal(err)
	}
	config := &goLicenseConfig{}
	if err := json.Unmarshal(configData, config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s (%w)", file, err)
	}
	return config, nil
}

func NewLicenseReport(config *goLicenseConfig) (*licenseReport, error) {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, err
	}
	return &licenseReport{
		goPath: goPath,
		cwd:    cwd,
		config: config,
	}, nil
}

type licenseReport struct {
	goPath string
	cwd    string
	config *goLicenseConfig
}

func (l *licenseReport) ejectVendor() error {
	cmd := exec.Command(l.goPath, "mod", "vendor")
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func (l *licenseReport) removeVendor() error {
	if err := os.RemoveAll("vendor"); err != nil {
		return err
	}
	return nil
}

func (l *licenseReport) build() error {
	env := os.Environ()
	env = append(env, "GOOS=linux")

	cmd := &exec.Cmd{
		Path: l.goPath,
		Args: []string{
			l.goPath,
			"build",
			"-o",
			"containerssh",
			"./cmd/containerssh",
		},
		Dir:    l.cwd,
		Env:    env,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func (l *licenseReport) removeBuild() error {
	if err := os.RemoveAll("containerssh"); err != nil {
		return err
	}
	return nil
}

func (l *licenseReport) detectLicenses() (map[string]moduleLicense, error) {
	vsn, err := version.ReadExe("containerssh")
	if err != nil {
		return nil, fmt.Errorf("failed to read binary (%w)", err)
	}
	if vsn.ModuleInfo == "" {
		return nil, fmt.Errorf("no module info in binary (%w)", err)
	}
	mods, err := module.ParseExeData(vsn.ModuleInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to parse module info (%w)", err)
	}
	licenses := map[string]moduleLicense{}
	licensesOk := true
	for _, mod := range mods {
		if strings.HasPrefix("github.com/containerssh/", mod.Path) {
			continue
		}
		moduleData, err := l.processModule(mod)
		if err != nil {
			return nil, err
		}
		licenses[mod.Path] = moduleData
		if !moduleData.Accepted {
			licensesOk = false
		}
	}
	if !licensesOk {
		return licenses, fmt.Errorf("invalid licenses detected")
	}
	return licenses, nil
}

func (l *licenseReport) processModule(mod module.Module) (
	moduleLicense,
	error,
) {
	license, licenseModPath, licenseOk, err := l.findModPathLicense(mod)
	if err != nil {
		return moduleLicense{}, err
	}
	noticeFile, err := l.findNoticeFile(licenseModPath)
	if err != nil {
		return moduleLicense{}, err
	}
	notice, err := l.readNoticeFile(noticeFile)
	if err != nil {
		return moduleLicense{}, err
	}
	result := moduleLicense{
		Module:   mod.Path,
		License:  license,
		Accepted: licenseOk,
		Notice:   notice,
	}
	result.Print()
	return result, nil
}

func (l *licenseReport) findModPathLicense(mod module.Module) (string, string, bool, error) {
	modPaths := []string{
		path.Join(l.cwd, "vendor", mod.Path),
		path.Join(
			l.cwd,
			"vendor",
			mod.Path,
			regexp.MustCompile(`\..*$`).ReplaceAllString(mod.Version, ""),
		),
	}
	licenseFound := ""
	licenseOk := false
	licenseModPath := ""
	var lastError error
loop:
	for _, modPath := range modPaths {
		f, err := filer.FromDirectory(modPath)
		if err != nil {
			return "", "", false, fmt.Errorf(
				"failed to create filer for mod path %s (%w)",
				modPath,
				err,
			)
		}
		if overrideLicense, ok := l.config.Override[mod.Path]; ok {
			licenseFound = overrideLicense
			for _, allowedLicense := range l.config.Allow {
				if licenseFound == allowedLicense {
					licenseOk = true
					licenseModPath = modPath
					break loop
				}
			}
		} else {
			lastError = nil
			match, err := licensedb.Detect(f)
			if err != nil {
				lastError = err
				continue
			} else {
				for licenseName, licenseMatch := range match {
					if licenseMatch.Confidence > 0.9 {
						licenseFound = licenseName
						for _, allowedLicense := range l.config.Allow {
							if licenseName == allowedLicense {
								licenseOk = true
								licenseModPath = modPath
								break loop
							}
						}
						log.Printf("detected disallowed license %s for %s", licenseName, mod.Path)
					}
				}
			}
		}
	}
	if lastError != nil {
		log.Printf("failed to detect license for %s (%v)", mod.Path, lastError)
	}
	return licenseFound, licenseModPath, licenseOk, nil
}

func (l *licenseReport) findNoticeFile(modPath string) (string, error) {
	fileInfos, err := ioutil.ReadDir(modPath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %s (%w)", modPath, err)
	}
	noticeFile := ""
	for _, f := range fileInfos {
		if f.Name() == "NOTICE" {
			noticeFile = path.Join(modPath, f.Name())
			break
		}
		if strings.HasPrefix(f.Name(), "NOTICE.") {
			noticeFile = path.Join(modPath, f.Name())
			break
		}
	}
	return noticeFile, nil
}

func (l *licenseReport) readNoticeFile(noticeFile string) (string, error) {
	notice := ""
	if noticeFile != "" {
		f, err := os.Open(noticeFile)
		if err != nil {
			return "", fmt.Errorf("failed to read file %s (%w)", noticeFile, err)
		}
		data, err := ioutil.ReadAll(f)
		if err != nil {
			_ = f.Close()
			return "", fmt.Errorf("failed to read file %s (%w)", noticeFile, err)
		}
		_ = f.Close()
		notice = string(data)
	}
	return notice, nil
}

type moduleLicense struct {
	Module   string
	License  string
	Notice   string
	Accepted bool
}

func (l moduleLicense) Print() {
	sign := "❌"
	if l.Accepted {
		sign = "✔️"
	}
	log.Printf("%s %s has license %s", sign, l.Module, l.License)
}

func (l *licenseReport) Run() (map[string]moduleLicense, error) {
	if err := l.ejectVendor(); err != nil {
		return nil, err
	}
	defer func() {
		if err := l.removeVendor(); err != nil {
			log.Println(err.Error())
		}
	}()

	if err := l.build(); err != nil {
		return nil, err
	}
	defer func() {
		if err := l.removeBuild(); err != nil {
			log.Println(err.Error())
		}
	}()

	return l.detectLicenses()
}

func renderNotice(licenses map[string]moduleLicense) []byte {
	var finalNotice bytes.Buffer
	finalNotice.Write([]byte("# Third party licenses\n\n"))
	finalNotice.Write([]byte("This project contains third party packages under the following licenses:\n\n"))
	for packageName, license := range licenses {
		finalNotice.Write([]byte(fmt.Sprintf("## [%s](https://%s)\n\n", packageName, packageName)))
		finalNotice.Write([]byte(fmt.Sprintf("**License:** %s\n\n", license.License)))

		trimmedNotice := strings.TrimSpace(license.Notice)
		if trimmedNotice != "" {
			finalNotice.Write([]byte(fmt.Sprintf("%s\n\n", "> "+strings.ReplaceAll(trimmedNotice, "\n", "\n> "))))
		}
	}
	noticeContents := finalNotice.Bytes()
	return noticeContents
}
