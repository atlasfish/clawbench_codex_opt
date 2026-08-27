package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeCodexRollout(t *testing.T, root, day, id, cwd string, updatedAt time.Time) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", filepath.FromSlash(day))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	file := filepath.Join(dir, fmt.Sprintf("rollout-2026-08-27T10-00-00-%s.jsonl", id))
	content := fmt.Sprintf(
		`{"timestamp":"2026-08-27T10:00:00.000Z","type":"session_meta","payload":{"id":%q,"timestamp":"2026-08-27T10:00:00.000Z","cwd":%q,"originator":"codex_cli_rs","cli_version":"1.0.0"}}`+"\n"+
			`{"timestamp":"2026-08-27T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"sensitive text must not be parsed"}]}}`+"\n",
		id, cwd,
	)
	require.NoError(t, os.WriteFile(file, []byte(content), 0o644))
	require.NoError(t, os.Chtimes(file, updatedAt, updatedAt))
	return file
}

func TestScanCodexSessionsFiltersProjectAndSortsNewestFirst(t *testing.T) {
	codexHome := t.TempDir()
	project := filepath.Join(t.TempDir(), "Project With Spaces")
	older := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)

	writeCodexRollout(t, codexHome, "2026/08/26", "00000000-0000-4000-8000-000000000001", project, older)
	writeCodexRollout(t, codexHome, "2026/08/27", "00000000-0000-4000-8000-000000000002", project, newer)
	writeCodexRollout(t, codexHome, "2026/08/27", "00000000-0000-4000-8000-000000000003", filepath.Join(t.TempDir(), "other"), newer.Add(time.Hour))

	sessions, stats := scanCodexSessions(filepath.Join(codexHome, "sessions"), project, 100, 100)

	require.Len(t, sessions, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000002", sessions[0].SessionID)
	assert.Equal(t, "00000000-0000-4000-8000-000000000001", sessions[1].SessionID)
	assert.Equal(t, 3, stats.scanned)
}

func TestScanCodexSessionsSkipsMalformedAndMissingFields(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions", "2026", "08", "27")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "rollout-broken.jsonl"), []byte("{broken\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "rollout-missing.jsonl"),
		[]byte(`{"type":"session_meta","payload":{"id":"id-without-cwd"}}`+"\n"), 0o644))

	sessions, stats := scanCodexSessions(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(root)))), "C:/Work/Repo", 100, 100)

	assert.Empty(t, sessions)
	assert.Equal(t, 2, stats.skipped)
}

func TestCodexProjectPathsEqualWindowsVariants(t *testing.T) {
	assert.True(t, codexProjectPathsEqual(`C:\Work\Repo With Spaces`, `c:/work/repo with spaces/`))
	assert.True(t, codexProjectPathsEqual(`C:\Work\Repo\.\sub\..`, `c:/work/repo`))
	assert.True(t, codexProjectPathsEqual(`\\Server\Share\Repo`, `//server/share/repo/`))
	assert.False(t, codexProjectPathsEqual(`C:\Work\Repo`, `C:\Work\Other`))
}

func TestCodexProjectPathsEqualPreservesPOSIXCaseSensitivity(t *testing.T) {
	assert.True(t, codexProjectPathsEqual("/home/user/repo/", "/home/user/repo"))
	assert.False(t, codexProjectPathsEqual("/home/User/repo", "/home/user/repo"))
}

func TestScanCodexSessionsHonorsLimits(t *testing.T) {
	codexHome := t.TempDir()
	project := t.TempDir()
	for i := 0; i < 5; i++ {
		writeCodexRollout(t, codexHome, "2026/08/27",
			fmt.Sprintf("00000000-0000-4000-8000-00000000000%d", i), project,
			time.Date(2026, 8, 27, 10, i, 0, 0, time.UTC))
	}

	sessions, stats := scanCodexSessions(filepath.Join(codexHome, "sessions"), project, 3, 2)

	assert.Len(t, sessions, 2)
	assert.LessOrEqual(t, stats.scanned, 3)
}

func TestScanCodexSessionsAppliesResultLimitAfterUpdatedSort(t *testing.T) {
	codexHome := t.TempDir()
	project := t.TempDir()
	base := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		file := writeCodexRollout(t, codexHome, fmt.Sprintf("2026/08/%02d", 25+i),
			fmt.Sprintf("00000000-0000-4000-8000-00000000001%d", i), project, base.Add(time.Duration(i)*time.Hour))
		if i == 0 {
			require.NoError(t, os.Chtimes(file, base.Add(10*time.Hour), base.Add(10*time.Hour)))
		}
	}

	sessions, stats := scanCodexSessions(filepath.Join(codexHome, "sessions"), project, 100, 2)

	require.Len(t, sessions, 2)
	assert.Equal(t, "00000000-0000-4000-8000-000000000010", sessions[0].SessionID)
	assert.Equal(t, 3, stats.scanned)
}

func TestParseCodexSessionHeaderUsesMetadataID(t *testing.T) {
	codexHome := t.TempDir()
	project := t.TempDir()
	file := writeCodexRollout(t, codexHome, "2026/08/27",
		"00000000-0000-4000-8000-000000000009", project, time.Now())
	info, err := os.Stat(file)
	require.NoError(t, err)

	session, err := parseCodexSessionHeader(file, info)

	require.NoError(t, err)
	assert.Equal(t, "00000000-0000-4000-8000-000000000009", session.SessionID)
	assert.Equal(t, project, session.Cwd)
	assert.Equal(t, time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC), session.CreatedAt)
}
