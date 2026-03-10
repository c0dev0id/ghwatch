package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
// most likely to be a signed release build.
//
// APKs whose filename contains "debug" or "unsigned" are always rejected.
// Among the remaining candidates, the highest scorer wins:
//
//	+10 "signed"
//	 +5 "release"
//
// Returns an error if no APKs are found or all candidates are debug/unsigned.
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

	isReleaseBuild := func(p string) bool {
		n := strings.ToLower(filepath.Base(p))
		return !strings.Contains(n, "debug") && !strings.Contains(n, "unsigned")
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
		return s
	}

	var candidates []string
	var rejected []string
	for _, p := range all {
		if isReleaseBuild(p) {
			candidates = append(candidates, p)
		} else {
			rejected = append(rejected, filepath.Base(p))
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no signed release APK found — rejected: %s",
			strings.Join(rejected, ", "))
	}

	best := candidates[0]
	bestScore := score(candidates[0])
	for _, p := range candidates[1:] {
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

// -- GitHub artifact download ------------------------------------------------

// artifactInfo holds the ID and name of one GitHub Actions artifact.
type artifactInfo struct {
	ID   int
	Name string
}

// getGHToken retrieves the active GitHub token via the gh CLI.
func getGHToken() (string, error) {
	tok, err := runGH("auth", "token")
	if err != nil {
		return "", fmt.Errorf("gh auth token: %v", err)
	}
	return strings.TrimSpace(tok), nil
}

// listArtifacts returns artifacts for a workflow run, filtered by name when
// name is non-empty.
func listArtifacts(token, repoSlug string, runID int, name string) ([]artifactInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/actions/runs/%d/artifacts", repoSlug, runID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Artifacts []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"artifacts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	var out []artifactInfo
	for _, a := range body.Artifacts {
		if name == "" || a.Name == name {
			out = append(out, artifactInfo{ID: a.ID, Name: a.Name})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no artifacts found (filter: %q)", name)
	}
	return out, nil
}

// progressReader wraps an io.Reader and sends byte-level progress to ch.
type progressReader struct {
	r          io.Reader
	total      int64 // 0 = unknown
	downloaded int64
	ch         chan<- installProgressMsg
}

func (pr *progressReader) Read(p []byte) (n int, err error) {
	n, err = pr.r.Read(p)
	pr.downloaded += int64(n)
	// Non-blocking send: the TUI drains the channel on every update; if it
	// falls behind we just skip a frame rather than stalling the download.
	select {
	case pr.ch <- installProgressMsg{Downloaded: pr.downloaded, Total: pr.total}:
	default:
	}
	return
}

// downloadArtifactZip downloads a single GitHub artifact as a zip file into
// a temp file (caller must delete it) while streaming progress to ch.
func downloadArtifactZip(token, repoSlug string, artifactID int, ch chan<- installProgressMsg) (string, error) {
	// Step 1: ask the API for the redirect URL (302 → signed S3 URL).
	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/actions/artifacts/%d/zip", repoSlug, artifactID)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := noFollow.Do(req)
	if err != nil {
		return "", fmt.Errorf("artifact redirect: %v", err)
	}
	resp.Body.Close()

	s3URL := resp.Header.Get("Location")
	if s3URL == "" {
		return "", fmt.Errorf("no redirect from GitHub API (status %d)", resp.StatusCode)
	}

	// Step 2: stream from S3 with progress tracking.
	resp2, err := http.Get(s3URL) //nolint:noctx — best-effort download
	if err != nil {
		return "", fmt.Errorf("download: %v", err)
	}
	defer resp2.Body.Close()

	total := resp2.ContentLength
	if total < 0 {
		total = 0
	}

	tmp, err := os.CreateTemp("", "ghwatch-artifact-*.zip")
	if err != nil {
		return "", fmt.Errorf("tempfile: %v", err)
	}

	pr := &progressReader{r: resp2.Body, total: total, ch: ch}
	if _, err := io.Copy(tmp, pr); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("download stream: %v", err)
	}
	tmp.Close()
	return tmp.Name(), nil
}

// extractZip unpacks a zip file into dir.
func extractZip(zipPath, dir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		outPath := filepath.Join(dir, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(outPath, 0755) //nolint:errcheck
			continue
		}
		os.MkdirAll(filepath.Dir(outPath), 0755) //nolint:errcheck
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// -- Install pipeline --------------------------------------------------------

// installToChannel runs the full download → extract → adb install → launch
// pipeline as a goroutine. Progress and log lines are sent to ch; the final
// message always has Done == true.
//
// packageName accepts "" (auto-detect from manifest), "com.example.app"
// (monkey launch), or "com.example.app/.MainActivity" (am start -n).
func installToChannel(runID int, sha, repoSlug, packageName, artifactName string, ch chan<- installProgressMsg) {
	var log []string
	appendLog := func(line string) {
		log = append(log, line)
		ch <- installProgressMsg{LogLine: line}
	}
	fail := func(err error) {
		ch <- installProgressMsg{Done: true, Err: err, FinalLog: log}
	}

	// 1. Authenticate.
	token, err := getGHToken()
	if err != nil {
		appendLog("✗  " + err.Error())
		fail(err)
		return
	}

	// 2. List artifacts.
	if artifactName != "" {
		appendLog(fmt.Sprintf("↓  artifact %q from run #%d", artifactName, runID))
	} else {
		appendLog(fmt.Sprintf("↓  artifacts from run #%d", runID))
	}
	artifacts, err := listArtifacts(token, repoSlug, runID, artifactName)
	if err != nil {
		appendLog("✗  " + err.Error())
		fail(err)
		return
	}

	// When the user has not pinned a specific artifact, filter out anything
	// whose name looks like a debug or unsigned build before downloading.
	if artifactName == "" {
		var release []artifactInfo
		var skipped []string
		for _, a := range artifacts {
			n := strings.ToLower(a.Name)
			if strings.Contains(n, "debug") || strings.Contains(n, "unsigned") {
				skipped = append(skipped, a.Name)
			} else {
				release = append(release, a)
			}
		}
		if len(skipped) > 0 {
			appendLog(fmt.Sprintf("–  skipping debug/unsigned: %s", strings.Join(skipped, ", ")))
		}
		if len(release) == 0 {
			err := fmt.Errorf("no release artifacts found — all skipped: %s", strings.Join(skipped, ", "))
			appendLog("✗  " + err.Error())
			fail(err)
			return
		}
		artifacts = release
	}

	// 3. Download + extract each artifact into a single temp dir.
	tmpDir, err := os.MkdirTemp("", "ghwatch-apk-*")
	if err != nil {
		appendLog("✗  tempdir: " + err.Error())
		fail(fmt.Errorf("tempdir: %v", err))
		return
	}
	defer os.RemoveAll(tmpDir)

	for _, a := range artifacts {
		appendLog(fmt.Sprintf("↓  %s", a.Name))
		zipPath, err := downloadArtifactZip(token, repoSlug, a.ID, ch)
		if err != nil {
			appendLog("✗  " + err.Error())
			fail(err)
			return
		}
		if err := extractZip(zipPath, tmpDir); err != nil {
			os.Remove(zipPath)
			appendLog("✗  extract: " + err.Error())
			fail(fmt.Errorf("extract: %v", err))
			return
		}
		os.Remove(zipPath)
		// Signal that download is done so the progress bar clears.
		ch <- installProgressMsg{Downloaded: 0, Total: 0}
		appendLog("✓  extracted")
	}

	// 4. Locate best APK.
	apkPath, err := findAPK(tmpDir)
	if err != nil {
		appendLog("✗  " + err.Error())
		fail(err)
		return
	}
	appendLog("✓  APK: " + filepath.Base(apkPath))

	// 5. Install.
	appendLog("⟳  adb install -r " + filepath.Base(apkPath))
	if out, err := runADB("install", "-r", apkPath); err != nil {
		appendLog("✗  " + err.Error())
		fail(fmt.Errorf("adb install: %v", err))
		return
	} else {
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				appendLog("   " + l)
			}
		}
	}

	// 6. Resolve package name (from manifest if not provided).
	pkg := packageName
	if pkg == "" {
		if p, err := readPackageFromManifest(apkPath); err != nil {
			appendLog("–  manifest: " + err.Error())
		} else {
			pkg = p
			appendLog("   pkg: " + pkg + " (from manifest)")
		}
	}

	// 7. Launch.
	if pkg == "" {
		appendLog("–  launch skipped (package unknown)")
		ch <- installProgressMsg{Done: true, FinalLog: log}
		return
	}

	if strings.Contains(pkg, "/") {
		appendLog("⟳  adb shell am start -n " + pkg)
		if out, err := runADB("shell", "am", "start", "-n", pkg); err != nil {
			appendLog("✗  " + err.Error()) // launch failure is non-fatal
		} else {
			for _, l := range strings.Split(out, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					appendLog("   " + l)
				}
			}
		}
	} else {
		appendLog("⟳  adb shell monkey -p " + pkg)
		if out, err := runADB("shell", "monkey", "-p", pkg,
			"-c", "android.intent.category.LAUNCHER", "1"); err != nil {
			appendLog("✗  " + err.Error())
		} else if out != "" {
			appendLog("   " + out)
		}
	}

	ch <- installProgressMsg{Done: true, FinalLog: log}
}

// waitForInstallProgress returns a Cmd that reads one installProgressMsg from ch.
func waitForInstallProgress(ch <-chan installProgressMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

