package distro

import (
	"fmt"
	"goMH/core"
	"goMH/dependencies"
	"goMH/tui"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// FindPatches ищет патчи на сервере
func FindPatches(baseURL, version string) ([]core.PatchInfo, error) {
	fullVersion, err := getFullVersionString(version)
	if err != nil {
		if regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(version) {
			fullVersion = version
		} else {
			return nil, err
		}
	}

	targetURL := getPatchesPath(baseURL, fullVersion)
	fallbackURL := getPatchesFallbackPath(baseURL, fullVersion)

	foundPatches, hasPatchesDirOnly, err := fetchPatchesFromURL(targetURL)
	if err != nil {
		return nil, err
	}

	if len(foundPatches) == 0 || hasPatchesDirOnly {
		slog.Debug("Основной каталог патчей пуст, используем fallback", "primary_url", targetURL, "fallback_url", fallbackURL)
		foundPatches, _, err = fetchPatchesFromURL(fallbackURL)
		if err != nil {
			return nil, err
		}
	}

	// Возвращаем топ-4
	if len(foundPatches) > 4 {
		foundPatches = foundPatches[:4]
	}
	enrichPatchChangeNotes(foundPatches)
	return foundPatches, nil
}

// SelectPatchMenu показывает меню выбора (UI)
func SelectPatchMenu(patches []core.PatchInfo) (core.PatchInfo, error) {
	skipOption := core.PatchInfo{
		ShortName:   "SKIP",
		Description: "Не применять патч",
	}
	displayPatches := append([]core.PatchInfo{skipOption}, patches...)
	return tui.SelectPatch(displayPatches, "\n--- Выберите патч для установки ---")
}

// ApplyPatch применяет выбранный патч
func ApplyPatch(ctx core.TaskContext, am core.AssetManager, wu core.WinUtils, patch core.PatchInfo, installDir, backupBaseDir string) error {
	ctx.Info("Начало применения патча...")
	slog.Info("ApplyPatch started", "patch", patch.ShortName, "installDir", installDir)

	sevenZip, err := dependencies.NewClient(am, wu)
	if err != nil {
		return err
	}

	// 1. Восстановление бэкапа (если был)
	if err := restoreFromBackup(wu, installDir, backupBaseDir); err != nil {
		slog.Warn("Не удалось восстановить бэкап", "error", err)
	}

	// 2. Скачивание
	patchArchivePath := patchCachePath(am, patch)

	if _, err := am.DownloadHTTPWithProgress(patch.FullURL, patchArchivePath); err != nil {
		return err
	}

	// 3. Распаковка во временную (stage 1)
	tempRoot := filepath.Join(am.Cfg().RootPath, "temp")
	_ = os.MkdirAll(tempRoot, 0755)

	stage1Dir, _ := os.MkdirTemp(tempRoot, "patch_s1_*")
	defer os.RemoveAll(stage1Dir)

	ctx.Info("Распаковка основного архива...")
	if err := sevenZip.Extract(patchArchivePath, stage1Dir, true); err != nil {
		return fmt.Errorf("ошибка извлечения архива: %w", err)
	}

	// 4. Поиск вложенного архива (рекурсивно)
	// Ищем zip/7z, в названии которого есть "front"
	var workDir string

	nestedArchive, err := findNestedArchiveRecursive(stage1Dir, "front")
	if err == nil && nestedArchive != "" {
		ctx.Info(fmt.Sprintf("Найден вложенный архив: %s", filepath.Base(nestedArchive)))

		stage2Dir, _ := os.MkdirTemp(tempRoot, "patch_s2_*")
		defer os.RemoveAll(stage2Dir)

		if err := sevenZip.Extract(nestedArchive, stage2Dir, true); err != nil {
			return fmt.Errorf("ошибка распаковки вложенного архива: %w", err)
		}
		// Теперь работаем с распакованным вложенным архивом
		workDir = stage2Dir
	} else {
		// Вложенного архива нет, работаем с результатом первой распаковки
		workDir = stage1Dir
	}

	// 5. Определение "корня контента" (Content Root)
	// Нам нужно найти папку, где лежат сами файлы (dll, exe), игнорируя Backoffice и обертки
	contentDir, err := resolveContentRoot(workDir)
	if err != nil {
		return fmt.Errorf("не удалось определить структуру патча: %w", err)
	}

	// Финальная проверка на адекватность
	if !containsDll(contentDir) {
		ctx.Warn("Внимание: В итоговой папке патча не обнаружены .dll файлы. Структура может быть неверной.")
		slog.Warn("DLL not found in final content dir", "dir", contentDir)
	} else {
		slog.Info("Content dir validated (contains DLLs)", "dir", contentDir)
	}

	// 6. Бэкап
	ctx.Info("Создание бэкапа...")
	if err := createBackupFromDir(wu, contentDir, installDir, backupBaseDir, patch.BuildNumber); err != nil {
		return fmt.Errorf("ошибка бэкапа: %w", err)
	}

	// 7. Копирование
	ctx.Info("Применение патча...")
	if err := wu.CopyDir(contentDir, installDir); err != nil {
		return err
	}

	return nil
}

// resolveContentRoot ищет "настоящую" папку с файлами фронта.
// Логика:
// 1. Если есть папка "Front", "iikoFront" — заходим в неё.
// 2. Если есть "BackOffice", но нет "Front" рядом — это ошибка (патч не для того?).
// 3. Если только одна папка внутри — заходим в неё.
// 4. Иначе возвращаем текущую.
func resolveContentRoot(startDir string) (string, error) {
	currentDir := startDir

	// Ограничим глубину поиска, чтобы не зациклиться
	for i := 0; i < 3; i++ {
		entries, err := os.ReadDir(currentDir)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			return "", fmt.Errorf("пустая папка патча")
		}

		// 1. Ищем явную папку Front
		for _, e := range entries {
			if e.IsDir() {
				name := strings.ToLower(e.Name())
				// Если нашли папку с именем front - это то, что нам нужно
				if strings.Contains(name, "front") && !strings.Contains(name, "back") {
					slog.Debug("Найдена папка Front, проваливаемся", "name", e.Name())
					return filepath.Join(currentDir, e.Name()), nil
				}
			}
		}

		// 2. Если явной папки нет, но есть только одна папка — заходим в неё
		if len(entries) == 1 && entries[0].IsDir() {
			slog.Debug("Проваливаемся в единственную подпапку", "name", entries[0].Name())
			currentDir = filepath.Join(currentDir, entries[0].Name())
			continue
		}

		// 3. Если папок несколько, но нет явного Front, и есть DLL в корне — считаем, что мы на месте
		if containsDll(currentDir) {
			return currentDir, nil
		}

		// Если мы здесь, значит у нас куча файлов/папок, нет явного Front и нет DLL.
		// Скорее всего это корень, где лежат папки Front и BackOffice, но почему-то имя папки Front не сматчилось.
		// Попробуем еще раз поискать, может имя было специфичное.
		break
	}
	return currentDir, nil
}

// findNestedArchiveRecursive рекурсивно ищет архив по ключевому слову
func findNestedArchiveRecursive(root, keyword string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			lower := strings.ToLower(d.Name())
			if strings.Contains(lower, keyword) && (strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".7z")) {
				found = path
				return io.EOF // Нашли - прерываем
			}
		}
		return nil
	})

	if err == io.EOF {
		return found, nil
	}
	if found != "" {
		return found, nil
	}
	return "", os.ErrNotExist
}

// containsDll проверяет наличие .dll файлов
func containsDll(dir string) bool {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".dll") {
			return true
		}
	}
	return false
}

// restoreFromBackup восстанавливает из бэкапа (файлы копируются обратно)
func restoreFromBackup(wu core.WinUtils, installDir, backupBaseDir string) error {
	backups, err := filepath.Glob(filepath.Join(backupBaseDir, "backup_*"))
	if err != nil || len(backups) == 0 {
		return nil
	}
	sort.Strings(backups)
	backupDir := backups[len(backups)-1]

	slog.Info("Восстановление из бэкапа", "path", backupDir)
	return filepath.Walk(backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(backupDir, path)
		dest := filepath.Join(installDir, rel)
		_ = os.MkdirAll(filepath.Dir(dest), 0755)
		return wu.CopyFile(path, dest)
	})
}

// createBackupFromDir копирует существующие файлы из installDir в бэкап,
// если такие же файлы есть в patchDir (чтобы бэкапить только то, что заменяем)
func createBackupFromDir(wu core.WinUtils, patchDir, installDir, backupBaseDir string, buildNumber int) error {
	backupDir := filepath.Join(backupBaseDir, fmt.Sprintf("backup_%d", buildNumber))

	// Если бэкап уже есть, не перезаписываем (защита от повторного запуска на уже патченой версии)
	if _, err := os.Stat(backupDir); err == nil {
		return nil
	}
	_ = os.MkdirAll(backupDir, 0755)

	return filepath.Walk(patchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(patchDir, path)
		existing := filepath.Join(installDir, rel)
		backup := filepath.Join(backupDir, rel)

		if _, err := os.Stat(existing); err == nil {
			_ = os.MkdirAll(filepath.Dir(backup), 0755)
			_ = wu.CopyFile(existing, backup)
		}
		return nil
	})
}

func getFullVersionString(short string) (string, error) {
	versions := map[string]string{
		"927": "9.2.7014.0", "926": "9.2.6029.0", "928": "9.2.8035.0",
		"929": "9.2.9037.0", "936": "9.3.6065.0", "937": "9.3.7067.0", "946": "9.4.6046.0",
	}
	if v, ok := versions[short]; ok {
		return v, nil
	}
	return "", fmt.Errorf("unknown short version")
}

func getPatchesPath(baseURL, version string) string {
	return fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), version)
}

func getPatchesFallbackPath(baseURL, version string) string {
	return fmt.Sprintf("%s/Patches", getPatchesPath(baseURL, version))
}

func fetchPatchesFromURL(targetURL string) ([]core.PatchInfo, bool, error) {
	slog.Debug("Поиск патчей", "url", targetURL)

	resp, err := http.Get(targetURL)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("HTTP %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}

	foundPatches, hasPatchesDirOnly := parsePatchesHTML(targetURL, string(body))
	return foundPatches, hasPatchesDirOnly, nil
}

func enrichPatchChangeNotes(patches []core.PatchInfo) {
	for i := range patches {
		changeNote, err := fetchPatchChangeNote(patches[i].FullURL)
		if err != nil {
			slog.Debug("Не удалось загрузить patch-note", "url", patches[i].FullURL, "error", err)
			continue
		}
		patches[i].ChangeNote = changeNote
	}
}

func fetchPatchChangeNote(archiveURL string) (string, error) {
	ext := filepath.Ext(archiveURL)
	if ext == "" {
		return "", fmt.Errorf("не удалось определить расширение архива")
	}
	changeNoteURL := strings.TrimSuffix(archiveURL, ext) + ".txt"

	resp, err := http.Get(changeNoteURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	changeNote := formatPatchChangeNote(body)
	if strings.TrimSpace(changeNote) == "" {
		return "", fmt.Errorf("patch-note пуст")
	}
	return changeNote, nil
}

func formatPatchChangeNote(raw []byte) string {
	text := decodePatchChangeNote(raw)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(strings.TrimPrefix(text, "\uFEFF"))
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	fileLines, fixLines := splitPatchChangeNoteSections(lines)

	var formatted []string
	if len(fileLines) > 0 {
		formatted = append(formatted, "Файлы:")
		formatted = append(formatted, fileLines...)
	}
	if len(fixLines) > 0 {
		if len(formatted) > 0 {
			formatted = append(formatted, "")
		}
		formatted = append(formatted, "Исправления:")
		formatted = append(formatted, fixLines...)
	}

	return strings.Join(formatted, "\n")
}

func decodePatchChangeNote(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	decoded, err := charmap.Windows1251.NewDecoder().Bytes(raw)
	if err == nil && utf8.Valid(decoded) {
		return string(decoded)
	}
	return string(raw)
}

func splitPatchChangeNoteSections(lines []string) ([]string, []string) {
	var fileLines []string
	var fixLines []string
	inFixes := false
	seenFiles := false

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if seenFiles {
				inFixes = true
			}
			continue
		}

		if !inFixes {
			fileLines = append(fileLines, line)
			seenFiles = true
			continue
		}

		if cleaned := normalizePatchFixLine(line); cleaned != "" {
			fixLines = append(fixLines, cleaned)
		}
	}

	return fileLines, fixLines
}

func normalizePatchFixLine(line string) string {
	line = strings.TrimSpace(line)
	line = regexp.MustCompile(`^[0-9a-fA-F]{40}\s+`).ReplaceAllString(line, "")
	line = strings.TrimSpace(strings.Trim(line, `"`))
	if line == "" {
		return ""
	}

	re := regexp.MustCompile(`(?i)^(?:revert\s+)?(RMS-\d+)\s*(?:fixed:\s*)?(.*)$`)
	if matches := re.FindStringSubmatch(line); len(matches) == 3 {
		taskID := strings.ToUpper(strings.TrimSpace(matches[1]))
		desc := strings.TrimSpace(strings.Trim(matches[2], `"`))
		if desc == "" {
			return taskID
		}
		return taskID + " " + desc
	}

	re = regexp.MustCompile(`(?i)(RMS-\d+)\s+(.*)$`)
	if matches := re.FindStringSubmatch(line); len(matches) == 3 {
		taskID := strings.ToUpper(strings.TrimSpace(matches[1]))
		desc := strings.TrimSpace(strings.Trim(matches[2], `"`))
		if desc == "" {
			return taskID
		}
		return taskID + " " + desc
	}

	return line
}

func parsePatchesHTML(targetURL, body string) ([]core.PatchInfo, bool) {
	linkRegex := regexp.MustCompile(`<a href=\"([^\"]+)\"`)
	buildRegex := regexp.MustCompile(`build(?:[\s_]|%20)(\d+)\)`)

	var foundPatches []core.PatchInfo
	hasPatchesDirLink := false

	for _, match := range linkRegex.FindAllStringSubmatch(body, -1) {
		encodedFileName := match[1]
		lowerName := strings.ToLower(encodedFileName)

		if strings.TrimSuffix(strings.Trim(lowerName, "/"), "/") == "patches" {
			hasPatchesDirLink = true
		}

		if !(strings.HasSuffix(lowerName, ".7z") || strings.HasSuffix(lowerName, ".zip")) {
			continue
		}
		buildMatches := buildRegex.FindStringSubmatch(encodedFileName)
		if len(buildMatches) < 2 {
			continue
		}
		buildNumber, _ := strconv.Atoi(buildMatches[1])
		firstWord := strings.Split(encodedFileName, "-")[0]
		shortName := fmt.Sprintf("%s-%d", firstWord, buildNumber)
		decodedFileName, _ := url.QueryUnescape(encodedFileName)
		fullURL, _ := url.JoinPath(targetURL, encodedFileName)

		foundPatches = append(foundPatches, core.PatchInfo{
			ShortName:   shortName,
			Description: decodedFileName,
			FullURL:     fullURL,
			BuildNumber: buildNumber,
		})
	}

	sort.Slice(foundPatches, func(i, j int) bool { return foundPatches[i].BuildNumber > foundPatches[j].BuildNumber })
	return foundPatches, hasPatchesDirLink && len(foundPatches) == 0
}
