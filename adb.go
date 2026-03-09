package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	tea "github.com/charmbracelet/bubbletea"
)

// -- Low-level runner --------------------------------------------------------

// runADB runs an adb command and returns (stdout, error).
// ADB is notorious for exiting 0 while embedding "Failure [...]" in stdout,
// so we inspect stdout in addition to the exit code.
func runADB(args ...string) (string, error) {
	cmd := exec.Command("adb", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()

	outStr := strings.TrimSpace(out.String())
	errStr := strings.TrimSpace(stderr.String())

	if err != nil {
		msg := errStr
		if msg == "" {
			msg = outStr
		}
		if msg == "" {
			msg = err.Error()
		}
		return outStr, fmt.Errorf("%s", msg)
	}

	// adb install exits 0 but puts "Failure [REASON]" on stdout on error.
	if strings.Contains(outStr, "Failure [") || strings.Contains(outStr, "Exception occurred") {
		return outStr, fmt.Errorf("%s", outStr)
	}
	return outStr, nil
}

// -- APK helpers -------------------------------------------------------------

// findAPK walks dir recursively, collects all .apk files, and returns the one
// most likely to be a signed release build.  Scoring:
//
//	+10 "signed"
//	 +5 "release"
//	-10 "debug"
//	 -3 "unsigned"
//
// When scores are equal the first file found wins.
func findAPK(dir string) (string, error) {
	var all []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".apk") {
			all = append(all, path)
		}
		return nil
	})
	if len(all) == 0 {
		return "", fmt.Errorf("no .apk file found in downloaded artifacts")
	}
	score := func(p string) int {
		n := strings.ToLower(filepath.Base(p))
		s := 0
		if strings.Contains(n, "signed") {
			s += 10
		}
		if strings.Contains(n, "release") {
			s += 5
		}
		if strings.Contains(n, "debug") {
			s -= 10
		}
		if strings.Contains(n, "unsigned") {
			s -= 3
		}
		return s
	}
	best := all[0]
	bestScore := score(all[0])
	for _, p := range all[1:] {
		if s := score(p); s > bestScore {
			bestScore = s
			best = p
		}
	}
	return best, nil
}

// readPackageFromManifest opens an APK (which is a ZIP), extracts the binary
// AndroidManifest.xml and parses it to find the package attribute.
// No external tools (aapt, aapt2, apkanalyzer) are required.
func readPackageFromManifest(apkPath string) (string, error) {
	zr, err := zip.OpenReader(apkPath)
	if err != nil {
		return "", fmt.Errorf("open APK: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name != "AndroidManifest.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open manifest: %w", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return "", fmt.Errorf("read manifest: %w", err)
		}
		return axmlPackage(data)
	}
	return "", fmt.Errorf("AndroidManifest.xml not found in APK")
}

// -- Binary XML (AXML) parser ------------------------------------------------
//
// Android stores AndroidManifest.xml as binary XML (AXML) inside the APK.
// The format is a sequence of typed chunks; we only need the string pool
// and the first START_ELEMENT (always <manifest>) to extract the package attr.

const (
	axmlChunkXML         = 0x0003
	axmlChunkStringPool  = 0x0001
	axmlChunkResMap      = 0x0180
	axmlChunkStartElem   = 0x0102
	axmlFlagUTF8         = 0x100
	axmlTypeString       = 0x03
)

// axmlPackage parses a binary AXML blob and returns the package attribute of
// the root <manifest> element.
func axmlPackage(data []byte) (string, error) {
	if len(data) < 8 {
		return "", fmt.Errorf("AXML too short")
	}
	if binary.LittleEndian.Uint16(data[0:]) != axmlChunkXML {
		return "", fmt.Errorf("not AXML (magic %04x)", binary.LittleEndian.Uint16(data[0:]))
	}

	// Walk top-level chunks.
	fileHeaderSize := int(binary.LittleEndian.Uint16(data[2:]))
	var pool []string

	for pos := fileHeaderSize; pos+8 <= len(data); {
		cType := binary.LittleEndian.Uint16(data[pos:])
		cSize := int(binary.LittleEndian.Uint32(data[pos+4:]))
		if cSize < 8 || pos+cSize > len(data) {
			break
		}

		switch cType {
		case axmlChunkStringPool:
			pool = axmlStrings(data[pos : pos+cSize])

		case axmlChunkStartElem:
			if len(pool) == 0 {
				break
			}
			// Layout within the chunk:
			//   [0:8]   ResChunk_header  (type, headerSize, size)
			//   [8:12]  lineNumber
			//   [12:16] comment (string ref)
			//   [16:20] ns     (string ref)  ← ResXMLTree_attrExt begins here
			//   [20:24] name   (string ref)
			//   [24:26] attributeStart  (uint16, offset from [16] to first attr)
			//   [26:28] attributeSize   (uint16, always 20)
			//   [28:30] attributeCount  (uint16)
			if pos+30 > len(data) {
				break
			}
			nameIdx := int32(binary.LittleEndian.Uint32(data[pos+20:]))
			if nameIdx < 0 || int(nameIdx) >= len(pool) || pool[nameIdx] != "manifest" {
				break // not <manifest>, keep scanning
			}

			attrStart := int(binary.LittleEndian.Uint16(data[pos+24:]))
			attrCount := int(binary.LittleEndian.Uint16(data[pos+28:]))
			// Attributes begin at: start of ResXMLTree_attrExt (pos+16) + attrStart
			attrBase := pos + 16 + attrStart

			for i := 0; i < attrCount; i++ {
				a := attrBase + i*20
				if a+20 > len(data) {
					break
				}
				// Each ResXMLTree_attribute (20 bytes):
				//   [0:4]  ns         (string ref)
				//   [4:8]  name       (string ref)
				//   [8:12] rawValue   (string ref, -1 = absent)
				//   [12:14] valueSize (uint16)
				//   [14:15] res0
				//   [15:16] dataType
				//   [16:20] data      (payload; if dataType==TYPE_STRING: string ref)
				aName := int32(binary.LittleEndian.Uint32(data[a+4:]))
				if aName < 0 || int(aName) >= len(pool) || pool[aName] != "package" {
					continue
				}
				// Prefer raw string value when present.
				raw := int32(binary.LittleEndian.Uint32(data[a+8:]))
				if raw >= 0 && int(raw) < len(pool) {
					return pool[raw], nil
				}
				// Fall back to typed value.
				if data[a+15] == axmlTypeString {
					idx := int32(binary.LittleEndian.Uint32(data[a+16:]))
					if idx >= 0 && int(idx) < len(pool) {
						return pool[idx], nil
					}
				}
			}
			return "", fmt.Errorf("package attribute not found in <manifest>")
		}

		pos += cSize
	}
	return "", fmt.Errorf("<manifest> element not found")
}

// axmlStrings parses an AXML string pool chunk and returns all strings.
func axmlStrings(chunk []byte) []string {
	// ResStringPool_header layout:
	//   [0:2]  type (0x0001)
	//   [2:4]  headerSize
	//   [4:8]  size
	//   [8:12] stringCount
	//   [12:16] styleCount
	//   [16:20] flags
	//   [20:24] stringsStart  (byte offset from start of chunk to string data)
	//   [24:28] stylesStart
	//   [28:]  uint32 offsets[stringCount]
	if len(chunk) < 28 {
		return nil
	}
	count := int(binary.LittleEndian.Uint32(chunk[8:]))
	flags := binary.LittleEndian.Uint32(chunk[16:])
	strStart := int(binary.LittleEndian.Uint32(chunk[20:]))
	isUTF8 := flags&axmlFlagUTF8 != 0

	result := make([]string, count)
	for i := 0; i < count; i++ {
		offIdx := 28 + i*4
		if offIdx+4 > len(chunk) {
			break
		}
		off := strStart + int(binary.LittleEndian.Uint32(chunk[offIdx:]))
		if off >= len(chunk) {
			continue
		}
		if isUTF8 {
			result[i] = axmlReadUTF8(chunk, off)
		} else {
			result[i] = axmlReadUTF16(chunk, off)
		}
	}
	return result
}

// axmlReadUTF16 reads a UTF-16LE encoded string from the string pool data.
// Format: uint16 charCount, charCount×uint16 code units, uint16(0) terminator.
// When the high bit of the first uint16 is set the length is stored in 2 shorts.
func axmlReadUTF16(data []byte, off int) string {
	if off+2 > len(data) {
		return ""
	}
	charLen := int(binary.LittleEndian.Uint16(data[off:]))
	off += 2
	if charLen&0x8000 != 0 {
		// High bit set: this is the high 15 bits; next uint16 has the low 16 bits.
		if off+2 > len(data) {
			return ""
		}
		charLen = ((charLen & 0x7fff) << 16) | int(binary.LittleEndian.Uint16(data[off:]))
		off += 2
	}
	end := off + charLen*2
	if end > len(data) {
		return ""
	}
	u16 := make([]uint16, charLen)
	for j := range u16 {
		u16[j] = binary.LittleEndian.Uint16(data[off+j*2:])
	}
	return string(utf16.Decode(u16))
}

// axmlReadUTF8 reads a UTF-8 encoded string from the string pool data.
// Format: (char count, variable-length encoded), (byte count, variable-length
// encoded), UTF-8 bytes, null terminator.
func axmlReadUTF8(data []byte, off int) string {
	if off >= len(data) {
		return ""
	}
	// Read UTF-16 character count (not needed, skip).
	charLen := int(data[off])
	off++
	if charLen&0x80 != 0 {
		if off >= len(data) {
			return ""
		}
		charLen = ((charLen & 0x7f) << 8) | int(data[off])
		off++
	}
	_ = charLen

	// Read UTF-8 byte count.
	if off >= len(data) {
		return ""
	}
	byteLen := int(data[off])
	off++
	if byteLen&0x80 != 0 {
		if off >= len(data) {
			return ""
		}
		byteLen = ((byteLen & 0x7f) << 8) | int(data[off])
		off++
	}
	end := off + byteLen
	if end > len(data) {
		return ""
	}
	return string(data[off:end])
}

// -- Install command ---------------------------------------------------------

// installFromRun downloads the APK artifact for runID, installs it via adb,
// and launches the app.  Every step is recorded in the returned log.
//
// artifactName, when non-empty, limits the download to that specific GitHub
// Actions artifact (e.g. "app-signed").  When empty all artifacts are
// downloaded and the best APK is selected by score.
//
// packageName accepts two formats:
//
//	"com.example.app"                – launches via `adb shell monkey`
//	"com.example.app/.MainActivity" – launches via `adb shell am start -n`
//
// If packageName is empty, the package name is read directly from the APK's
// AndroidManifest.xml (no external tools required).  The activity class
// cannot be auto-detected; launch falls back to monkey in that case.
func installFromRun(runID int, sha, packageName, artifactName string) tea.Cmd {
	return func() tea.Msg {
		var log []string
		appendLog := func(line string) { log = append(log, line) }

		fail := func(err error) tea.Msg {
			return adbInstallMsg{sha: sha, err: err, log: log}
		}

		// 1. Download artifact(s)
		dlArgs := []string{"run", "download", fmt.Sprintf("%d", runID), "--dir", ""}
		if artifactName != "" {
			appendLog(fmt.Sprintf("↓  gh run download %d --name %s", runID, artifactName))
			dlArgs = []string{"run", "download", fmt.Sprintf("%d", runID), "--name", artifactName, "--dir", ""}
		} else {
			appendLog(fmt.Sprintf("↓  gh run download %d (all artifacts)", runID))
			dlArgs = []string{"run", "download", fmt.Sprintf("%d", runID), "--dir", ""}
		}

		tmpDir, err := os.MkdirTemp("", "vibeDev-apk-*")
		if err != nil {
			appendLog("✗  tempdir: " + err.Error())
			return fail(fmt.Errorf("tempdir: %v", err))
		}
		defer os.RemoveAll(tmpDir)

		// Fill in the tmpDir placeholder at the end of dlArgs.
		dlArgs[len(dlArgs)-1] = tmpDir
		if out, err := runGH(dlArgs...); err != nil {
			appendLog("✗  " + err.Error())
			return fail(fmt.Errorf("artifact download: %v", err))
		} else if out != "" {
			appendLog("   " + out)
		}

		// 2. Locate APK
		apkPath, err := findAPK(tmpDir)
		if err != nil {
			appendLog("✗  " + err.Error())
			return fail(err)
		}
		appendLog("✓  APK: " + filepath.Base(apkPath))

		// 3. Install (upgrade if already present; -r = replace)
		appendLog("⟳  adb install -r " + filepath.Base(apkPath))
		if out, err := runADB("install", "-r", apkPath); err != nil {
			appendLog("✗  " + err.Error())
			return fail(fmt.Errorf("adb install: %v", err))
		} else {
			for _, l := range strings.Split(out, "\n") {
				l = strings.TrimSpace(l)
				if l != "" {
					appendLog("   " + l)
				}
			}
		}

		// 4. Resolve package / component for launch
		pkg := packageName
		if pkg == "" {
			p, err := readPackageFromManifest(apkPath)
			if err != nil {
				appendLog("–  manifest: " + err.Error())
			} else {
				pkg = p
				appendLog("   pkg: " + pkg + " (from manifest)")
			}
		}

		// 5. Launch
		if pkg == "" {
			appendLog("–  launch skipped (package unknown)")
			return adbInstallMsg{sha: sha, err: nil, log: log}
		}

		if strings.Contains(pkg, "/") {
			// Component "pkg/.Activity" → am start -n
			appendLog("⟳  adb shell am start -n " + pkg)
			if out, err := runADB("shell", "am", "start", "-n", pkg); err != nil {
				appendLog("✗  " + err.Error())
				// Launch failure is non-fatal; install still succeeded.
			} else {
				for _, l := range strings.Split(out, "\n") {
					l = strings.TrimSpace(l)
					if l != "" {
						appendLog("   " + l)
					}
				}
			}
		} else {
			// Package only → trigger LAUNCHER intent via monkey
			appendLog("⟳  adb shell monkey -p " + pkg)
			if out, err := runADB("shell", "monkey", "-p", pkg,
				"-c", "android.intent.category.LAUNCHER", "1"); err != nil {
				appendLog("✗  " + err.Error())
			} else if out != "" {
				appendLog("   " + out)
			}
		}

		return adbInstallMsg{sha: sha, err: nil, log: log}
	}
}

