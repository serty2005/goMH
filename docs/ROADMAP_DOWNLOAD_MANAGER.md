# goMH Roadmap: надёжный download-manager subsystem для дистрибутивов, FTP fallback, BITS и S3/MinIO

## 0. Назначение документа

Этот документ является рабочим roadmap и набором промптов для кодовых агентов, которые будут дорабатывать проект `goMH`.

Цель изменений: превратить текущий механизм скачивания ресурсов и дистрибутивов в устойчивую download-manager подсистему, которая корректно работает на плохом и медленном интернете, переживает обрывы связи, поддерживает докачку, fallback-источники, корректную проверку результата и интеграцию с Windows BITS.

Документ самодостаточный: агент должен использовать его как источник требований, архитектурных решений, этапов реализации и критериев приёмки.

---

## 1. Контекст проекта

`goMH` — Go-утилита для задач техподдержки. В проекте уже есть рабочий механизм загрузки ресурсов через `assetmgr`.

Текущее состояние по известной реализации:

- ресурсы описываются в конфиге через `AssetCatalog`;
- есть HTTP-загрузка с progress;
- есть FTP-загрузка с progress;
- есть retry;
- есть HTTP resume через `Range`;
- есть FTP resume через `RetrFrom`;
- есть проверка итогового размера;
- есть runtime hooks для статуса, progress и cancel;
- есть кэш ресурсов;
- есть отдельная логика обработки загруженного ресурса: распаковка zip или размещение файла.

Текущую рабочую функциональность нельзя ломать. Изменения должны быть постепенными и тестируемыми.

---

## 2. Принятые архитектурные решения

### 2.1. Не делаем

Следующие варианты не входят в roadmap и не должны планироваться агентом:

- не делаем CDN/Cloudflare перед S3/MinIO;
- не делаем подпись `manifest.json` приватным ключом;
- не делаем parallel segmented download;
- не делаем зеркалирование внешних iiko/syrve HTTP-дистрибутивов в собственное S3;
- не делаем историю версий `manifest.json` как отдельную подсистему;
- не делаем BITS единственным механизмом скачивания.

### 2.2. Делаем

Делаем download-manager subsystem внутри `assetmgr`:

```text
assetmgr/
  manager.go
  downloader/
    job.go
    manager.go
    http.go
    ftp.go
    bits_windows.go
    bits_stub.go
    verify.go
    retry.go
    meta.go
    progress.go
```

Допустимо скорректировать структуру файлов, если в текущем проекте уже есть более подходящее место, но логические границы должны сохраниться.

---

## 3. Целевая модель скачивания

### 3.1. Общая схема

```text
assetmgr.Manager.DownloadToCache(assetName)
  -> собрать DownloadJob
  -> downloader.Manager.Download(ctx, job)
      -> проверить финальный файл
      -> проверить .part/.meta
      -> выбрать source
      -> выбрать backend
      -> скачать с resume/retry/backoff/progress
      -> проверить размер
      -> проверить sha256, если он задан и обязателен
      -> atomic finalize
  -> вернуть localCachePath
```

### 3.2. Файлы состояния

Для каждого скачивания должны использоваться:

```text
cache/
  file.exe
  file.exe.part
  file.exe.meta.json
```

Где:

- `file.exe` — финальный файл, пригодный к использованию;
- `file.exe.part` — частично или полностью скачанный файл, ещё не прошедший финальную проверку;
- `file.exe.meta.json` — состояние загрузки, позволяющее корректно продолжить после обрыва или перезапуска приложения.

Финальный файл не должен появляться до успешной проверки.

### 3.3. Atomic finalize

После успешной загрузки:

1. проверить размер;
2. проверить SHA256, если он требуется;
3. удалить старый финальный файл, если есть;
4. переименовать `.part` в финальный путь;
5. удалить `.meta.json`.

На Windows не полагаться на замену существующего файла через `os.Rename`. Сначала удалять финальный файл, затем делать rename.

---

## 4. Политики по типам ресурсов

### 4.1. Внешние iiko/syrve HTTP-дистрибутивы

Для внешних HTTP-ресурсов iiko/syrve нет доверенного SHA256.

Политика:

```text
primary source: внешний HTTP
fallback source: соответствующий FTP-сервер
validation: точный размер до байта
sha256: не требуется
BITS: разрешён для HTTP/HTTPS на Windows
```

Надёжность строится на:

- HTTP `Range`;
- FTP `RetrFrom`;
- retry с backoff;
- fallback HTTP -> FTP;
- сохранении `.part/.meta`;
- проверке итогового размера;
- проверке согласованности размера между источниками, когда это возможно.

Если HTTP-источник сообщил размер, а FTP-источник сообщает другой размер для того же дистрибутива, агент не должен молча продолжать установку. Это конфликт источников. Нужно вернуть ошибку с понятным диагностическим сообщением.

### 4.2. Собственные ресурсы, которые сейчас раздаются через `f.serty.top`

Эти ресурсы должны быть переведены на S3/MinIO-backed endpoint.

Важно: S3 будет на отдельном ресурсе. Креды и конкретные параметры подключения будут переданы агенту отдельно в рабочей сессии. В коде нельзя хардкодить креды.

Политика:

```text
primary source: HTTPS endpoint поверх S3/MinIO
validation: точный размер + SHA256
sha256: обязателен для критичных собственных ресурсов
BITS: разрешён для HTTPS на Windows
```

Утилита не должна требовать постоянных S3 access key/secret key для обычного скачивания на клиентских машинах. Предпочтительная модель:

```text
S3/MinIO хранит объекты
публичный или авторизованный HTTP endpoint отдаёт файлы
goMH скачивает по обычному HTTPS URL
manifest содержит url, size, sha256
```

Если агенту в отдельной сессии дадут S3-креды, их использовать только для проверки доступности, генерации manifest, загрузки тестовых объектов или dev-инструментов, но не встраивать в клиентскую утилиту.

### 4.3. Малые config/manifest файлы

Для config/manifest файлов:

```text
BITS: не нужен
retry: нужен
общий timeout: допустим короткий только для малых файлов, но не переиспользовать его для больших дистрибутивов
validation: JSON parse + schema validation
```

Если текущая загрузка конфига использует `http.Get`, её желательно перевести на общий HTTP-клиент/ retry-обёртку, но не смешивать с long-running large-file download timeout.

---

## 5. Таймауты

### 5.1. Запрещено

Запрещено ставить общий timeout на всё скачивание большого файла.

Нельзя делать так:

```go
client := &http.Client{
    Timeout: 10 * time.Minute,
}
```

Для больших дистрибутивов на медленном интернете скачивание может длиться часами. Пока байты продолжают поступать, загрузка должна продолжаться.

### 5.2. Разрешено и нужно

Нужно использовать:

- timeout на TCP/TLS connect;
- timeout на ожидание HTTP response headers;
- idle watchdog на отсутствие новых байт;
- context cancel по пользовательской отмене;
- retry/backoff после обрыва или stall.

Пример целевой модели HTTP-клиента:

```go
func NewLargeFileHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,

			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,

			TLSHandshakeTimeout:   20 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,

			DisableCompression: true,
		},
		Timeout: 0,
	}
}
```

---

## 6. Retry policy

Текущий фиксированный retry должен быть заменён на configurable retry policy.

Рекомендуемые значения по умолчанию:

```go
RetryPolicy{
	MaxAttempts:      50,
	BaseDelay:        1 * time.Second,
	MaxDelay:         60 * time.Second,
	IdleTimeout:      90 * time.Second,
	SourceFailoverAt: 8,
}
```

### 6.1. Retryable ошибки

К retryable относятся:

```text
connection reset
unexpected EOF
timeout
temporary DNS error
TLS handshake timeout
HTTP 408
HTTP 425
HTTP 429
HTTP 500
HTTP 502
HTTP 503
HTTP 504
stalled download
```

### 6.2. Terminal ошибки

К terminal относятся:

```text
invalid URL
unsupported protocol
HTTP 400
HTTP 401
HTTP 403
HTTP 404
local permission denied
disk full
final size mismatch after all sources
sha256 mismatch for own S3/MinIO assets
```

Примечание: `404` на одном fallback-source может означать переход к следующему source, но не должен ретраиться 50 раз на том же source.

---

## 7. Source chain

Нужно отказаться от восприятия HTTP и FTP как взаимоисключающих режимов для дистрибутивов iiko/syrve.

Целевая модель:

```go
type DownloadSource struct {
	Kind       SourceKind
	URL        string
	FTPConfig  *config.FTPConfig
	FTPPath    string
	Priority   int
	IsFallback bool
}

type SourceKind string

const (
	SourceHTTP SourceKind = "http"
	SourceFTP  SourceKind = "ftp"
	SourceS3   SourceKind = "s3"
)
```

Для iiko/syrve job должен строиться так:

```text
sources:
  1. vendor HTTP
  2. configured FTP fallback
```

Для собственных S3/MinIO ресурсов:

```text
sources:
  1. HTTPS URL поверх S3/MinIO
```

Если в конфиге будет задано несколько HTTP URLs, их можно использовать как последовательные fallback sources, но не планировать CDN или отдельную mirror-систему.

---

## 8. DownloadJob и связанные структуры

Целевые структуры:

```go
type DownloadJob struct {
	AssetName      string
	FileName       string
	FinalPath      string
	PartPath       string
	MetaPath       string
	Sources        []DownloadSource
	ExpectedSize   int64
	SHA256         string
	RequireSHA256  bool
	AllowBITS      bool
	RetryPolicy    RetryPolicy
	ValidationMode ValidationMode
}

type ValidationMode string

const (
	ValidationSizeOnly   ValidationMode = "size_only"
	ValidationSizeSHA256 ValidationMode = "size_sha256"
)

type DownloadResult struct {
	Path             string
	AlreadyComplete bool
	SourceURL        string
	SourceKind       string
	Bytes            int64
}

type DownloadMeta struct {
	AssetName    string    `json:"asset_name"`
	SourceURL    string    `json:"source_url"`
	SourceKind   string    `json:"source_kind"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	ExpectedSize int64     `json:"expected_size,omitempty"`
	SHA256       string    `json:"sha256,omitempty"`
	Downloaded   int64     `json:"downloaded"`
	StartedAt    time.Time `json:"started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastError     string    `json:"last_error,omitempty"`
}
```

Допускается адаптация имён под стиль текущего проекта, но смысл должен быть сохранён.

---

## 9. Config changes

Текущую структуру `AssetInfo` нужно расширить без поломки старых конфигов.

Целевая структура:

```go
type AssetInfo struct {
	URL            string   `json:"url"`
	URLs           []string `json:"urls,omitempty"`
	Type           string   `json:"type"`
	Destination    string   `json:"destination"`
	DownloadMethod string   `json:"download_method"`

	Size           int64    `json:"size,omitempty"`
	SHA256         string   `json:"sha256,omitempty"`

	Storage        string   `json:"storage,omitempty"`
	Bucket         string   `json:"bucket,omitempty"`
	ObjectKey      string   `json:"object_key,omitempty"`
	PublicURL      string   `json:"public_url,omitempty"`

	AllowBITS      *bool    `json:"allow_bits,omitempty"`
}
```

Правила совместимости:

- если задан только `url`, использовать его как единственный source;
- если задан `urls`, использовать их как source chain;
- если `sha256` пустой, не требовать SHA256;
- если `size` пустой, попытаться получить размер через HTTP HEAD/FTP size;
- если `allow_bits` не задан, использовать глобальную настройку;
- старые конфиги должны продолжить работать.

Также добавить глобальную download-конфигурацию:

```go
type DownloadConfig struct {
	PreferBITSOnWindows bool `json:"prefer_bits_on_windows"`
	FallbackToGoHTTP    bool `json:"fallback_to_go_http"`
	MaxAttempts         int  `json:"max_attempts"`
	BaseDelaySeconds    int  `json:"base_delay_seconds"`
	MaxDelaySeconds     int  `json:"max_delay_seconds"`
	IdleTimeoutSeconds  int  `json:"idle_timeout_seconds"`
	SourceFailoverAt    int  `json:"source_failover_at"`
}
```

Добавить поле в `Config`:

```go
Download DownloadConfig `json:"download"`
```

Обеспечить defaults, если блок `download` отсутствует.

---

## 10. HTTP downloader

### 10.1. Требования

HTTP backend должен:

- использовать custom `http.Client`;
- не иметь общего timeout на весь файл;
- поддерживать HEAD preflight;
- поддерживать GET с `Range`;
- сохранять `ETag` и `Last-Modified`;
- при resume использовать `If-Range`, если есть валидатор;
- корректно обрабатывать `200`, `206`, `416`;
- писать только в `.part`;
- обновлять `.meta.json`;
- обновлять progress;
- поддерживать cancel через context;
- поддерживать idle watchdog;
- классифицировать ошибки.

### 10.2. Поведение при статусах

```text
200 OK:
  если локальный offset = 0:
    скачать с начала
  если offset > 0:
    сервер не подтвердил resume
    удалить/перезаписать .part
    скачать с начала

206 Partial Content:
  проверить Content-Range
  если start == localSize:
    append в .part
  иначе:
    сбросить .part и начать заново

416 Requested Range Not Satisfiable:
  если Content-Range показывает, что localSize == totalSize:
    проверить .part как завершённый файл
  иначе:
    сбросить .part и начать заново или перейти к fallback source

4xx:
  terminal, кроме случаев, где есть следующий source

5xx / 408 / 425 / 429:
  retryable
```

---

## 11. FTP downloader

### 11.1. Требования

FTP backend должен:

- получать remote size через FTP `SIZE`, если возможно;
- сравнивать remote size и local `.part` size;
- поддерживать resume через `RetrFrom`;
- fallback на полную загрузку через `Retr`, если resume не поддержан;
- писать только в `.part`;
- проверять итоговый размер;
- обновлять progress;
- поддерживать context cancel;
- не логировать пароль и чувствительные параметры.

### 11.2. FTP fallback после HTTP

Если HTTP уже начал скачивание `.part`, затем произошёл failover на FTP:

```text
если FTP remote size == HTTP expected size:
  продолжить .part через RetrFrom

если FTP remote size != HTTP expected size:
  удалить .part/.meta
  скачать FTP с нуля
  в конце проверить размер
  если размер конфликтует с ожидаемым HTTP размером, вернуть ошибку
```

Для внешних iiko/syrve это важно, потому что нет SHA256.

---

## 12. BITS backend

### 12.1. Роль BITS

BITS — опциональный Windows-only backend для HTTP/HTTPS.

BITS не заменяет Go HTTP downloader полностью.

Правило выбора:

```text
если Windows
и source.Kind == http
и URL http/https
и job.AllowBITS == true
и global PreferBITSOnWindows == true:
  попробовать BITS
  если BITS недоступен и FallbackToGoHTTP == true:
    использовать Go HTTP
```

### 12.2. MVP-вариант

На первом этапе допустим PowerShell backend:

```text
Start-BitsTransfer -Source <url> -Destination <path> -TransferType Download -Priority Foreground
```

Но этот вариант считается временным, потому что progress и reattach контролируются хуже.

### 12.3. Целевой production-вариант

Целевой backend должен использовать Windows COM API:

```text
IBackgroundCopyManager
IBackgroundCopyJob
AddFile
Resume
GetProgress
GetState
Complete
Cancel
```

Рекомендуемая Go-библиотека для COM:

```text
github.com/go-ole/go-ole
```

BITS backend должен быть изолирован в файле:

```go
//go:build windows
```

Для остальных платформ:

```go
//go:build !windows
```

stub должен возвращать `ErrBackendUnavailable`.

### 12.4. BITS progress

BITS backend должен встроиться в текущий интерфейс progress:

```text
каждые 500 мс:
  получить bytesTransferred и bytesTotal
  вычислить процент
  вызвать ProgressSink
```

### 12.5. BITS output path

BITS должен скачивать не прямо в финальный файл, а во временный путь:

```text
file.exe.bits
```

После завершения:

```text
проверить размер
проверить sha256, если требуется
rename file.exe.bits -> file.exe
```

Если удобнее унифицировать, можно использовать `.part`, но нужно убедиться, что BITS не конфликтует с Go HTTP resume.

---

## 13. Progress interface

Сделать общий progress sink, который адаптируется к текущим runtime hooks.

Пример:

```go
type ProgressSink interface {
	Status(text string)
	Progress(description string, percent int)
	Cancelable(enabled bool)
	Logf(format string, args ...any)
}
```

`assetmgr.Manager` должен передать реализацию, которая вызывает текущие:

- `statusFn`;
- `percentFn`;
- `cancelFn`;
- stdout/progress writer.

Новая подсистема не должна напрямую зависеть от TUI, если это можно избежать.

---

## 14. Логирование и диагностика

Для каждого скачивания логировать:

```text
asset_name
file_name
source_kind
source_host
attempt
resume_offset
expected_size
downloaded_bytes
speed_avg
last_error
fallback_reason
validation_mode
final_result
```

Не логировать:

- FTP password;
- S3 secret;
- полный presigned URL query string;
- токены и креды.

Для URL в логах использовать host + path без query.

---

## 15. Тестирование

### 15.1. Unit tests

Покрыть:

```text
parse Content-Range
parse 416 Content-Range
retry delay with max cap
retryable/terminal classification
validate size
validate sha256
load/save meta
resume decision по meta
source failover decision
```

### 15.2. HTTP integration tests

Использовать `httptest.Server`.

Сценарии:

```text
1. download from zero
2. interrupted download -> resume via Range
3. server returns 200 instead of 206 -> restart
4. server returns 416 and local size == total -> success
5. server returns 416 and local size mismatch -> restart/failure
6. server changes ETag -> restart
7. server stalls -> idle watchdog cancels and retry resumes
8. 500 -> retry
9. 404 -> terminal/fallback
```

### 15.3. FTP tests

Если сложно поднять полноценный FTP server в тестах, вынести FTP client interface и мокать его.

Сценарии:

```text
1. full FTP download
2. resume via RetrFrom
3. RetrFrom unsupported -> Retr from zero
4. remote size mismatch -> error
5. cancel during copy
```

### 15.4. BITS tests

BITS не должен ломать CI на non-Windows.

Минимум:

```text
go test ./... на Linux должен проходить через bits_stub.go
go test ./... на Windows должен компилировать bits_windows.go
```

Реальные BITS integration tests можно оставить manual, если CI не Windows.

### 15.5. Manual tests

Обязательные ручные проверки:

```text
1. большой файл по HTTP на медленной сети
2. обрыв процесса во время скачивания
3. повторный запуск и докачка
4. отключение HTTP-источника и fallback на FTP
5. повреждение .part и корректный restart
6. повреждение final-файла и перекачка
7. S3/MinIO собственный ресурс с sha256
8. BITS download на Windows
9. BITS fallback to Go HTTP при недоступности BITS
```

---

## 16. Критерии готовности

Изменения считаются готовыми, если:

```text
1. Старые asset_catalog entries продолжают работать.
2. HTTP-дистрибутивы докачиваются после обрыва.
3. FTP-дистрибутивы докачиваются после обрыва.
4. HTTP -> FTP fallback работает для iiko/syrve.
5. Большие файлы не ограничены общим timeout.
6. При отсутствии новых байт idle watchdog прерывает попытку и запускает retry/resume.
7. Финальный файл появляется только после проверки.
8. .part/.meta сохраняются после обрыва и используются при следующем запуске.
9. Для внешних iiko/syrve проверяется точный размер.
10. Для собственных S3/MinIO ресурсов проверяется точный размер + sha256.
11. BITS backend работает на Windows или корректно fallback-ится на Go HTTP.
12. `go test ./...` проходит.
13. Не логируются секреты.
```

---

## 17. Итерационный план разработки

## Итерация 1. Downloader domain model и совместимый фасад

### Цель

Создать основу новой подсистемы без радикальной замены текущей логики.

### Задачи

- создать пакет `assetmgr/downloader`;
- добавить `DownloadJob`, `DownloadSource`, `DownloadResult`, `DownloadMeta`, `RetryPolicy`, `ValidationMode`;
- добавить `ProgressSink`;
- добавить defaults для `DownloadConfig`;
- расширить `config.AssetInfo` без поломки старых конфигов;
- подготовить builder, который из `AssetInfo` собирает `DownloadJob`;
- текущую загрузку оставить рабочей.

### Acceptance criteria

```text
go test ./... проходит
старый DownloadToCache продолжает работать
новые структуры покрыты базовыми unit tests
старые конфиги валидны
```

### Промпт для агента

```text
Ты работаешь в репозитории goMH.

Задача: выполнить Итерацию 1 из документа `ROADMAP_DOWNLOAD_MANAGER.md`.

Нужно создать основу новой download-manager подсистемы, не ломая текущую рабочую загрузку.

Сделай:
- новый пакет `assetmgr/downloader`;
- структуры DownloadJob, DownloadSource, DownloadResult, DownloadMeta, RetryPolicy, ValidationMode;
- интерфейс ProgressSink;
- расширение config.AssetInfo обратно совместимыми полями: URLs, Size, SHA256, Storage, Bucket, ObjectKey, PublicURL, AllowBITS;
- новую DownloadConfig в config.Config с defaults;
- builder, который из текущего AssetInfo может собрать DownloadJob;
- unit tests для defaults и базовой сборки job.

Важно:
- не удаляй текущие DownloadHTTPWithProgress и DownloadFTPWithProgress;
- не ломай старые конфиги;
- не добавляй реальные S3-креды;
- не реализуй BITS на этой итерации;
- не делай parallel segmented download.

После изменений запусти:
go test ./...

В итоговом отчёте на русском перечисли изменённые файлы, что сделано, какие тесты добавлены и какие команды проверки выполнены.
```

---

## Итерация 2. `.part/.meta`, validation и atomic finalize

### Цель

Перевести модель скачивания на безопасный кэш с `.part/.meta` и финализацией.

### Задачи

- реализовать `meta.go`: load/save/remove meta;
- реализовать `verify.go`: size validation, sha256 validation;
- реализовать atomic finalize;
- реализовать проверку финального файла перед скачиванием;
- реализовать проверку `.part/.meta` перед resume;
- добавить tests.

### Acceptance criteria

```text
final file не появляется до успешной проверки
битый final удаляется/перекачивается
.part/.meta используются после перезапуска
size validation работает
sha256 validation работает для собственных ресурсов
```

### Промпт для агента

```text
Ты работаешь в репозитории goMH.

Задача: выполнить Итерацию 2 из `ROADMAP_DOWNLOAD_MANAGER.md`.

Нужно реализовать безопасную файловую модель загрузки:
- final file;
- .part file;
- .meta.json;
- validation;
- atomic finalize.

Сделай:
- assetmgr/downloader/meta.go с load/save/remove meta;
- assetmgr/downloader/verify.go с проверкой размера и sha256;
- atomic finalize с учётом Windows: сначала удалить старый final, затем rename .part -> final;
- функцию проверки уже готового final-файла;
- функцию принятия решения по существующему .part/.meta;
- unit tests.

Важно:
- для внешних ресурсов sha256 может отсутствовать;
- если sha256 отсутствует, не требуй его;
- если sha256 указан и RequireSHA256=true, mismatch должен быть ошибкой;
- не меняй поведение ProcessFromCache;
- не добавляй BITS;
- не добавляй S3-клиент с кредами.

После изменений запусти:
go test ./...

Итоговый отчёт дай на русском.
```

---

## Итерация 3. HTTP downloader v2

### Цель

Реализовать устойчивый HTTP backend с Range resume, If-Range, custom client, idle watchdog, retry classification.

### Задачи

- создать `http.go`;
- реализовать custom large-file HTTP client без общего timeout;
- реализовать HEAD preflight;
- реализовать GET с Range;
- сохранять ETag/Last-Modified в meta;
- использовать If-Range при resume;
- корректно обрабатывать 200/206/416;
- реализовать idle watchdog;
- реализовать retryable/terminal classification;
- писать только в `.part`;
- финализировать только после проверки.

### Acceptance criteria

```text
HTTP download from zero работает
HTTP resume работает
200 вместо 206 приводит к restart
416 при полном локальном файле приводит к проверке и success
ETag change приводит к restart
idle stall приводит к retry/resume
нет общего timeout на весь файл
```

### Промпт для агента

```text
Ты работаешь в репозитории goMH.

Задача: выполнить Итерацию 3 из `ROADMAP_DOWNLOAD_MANAGER.md`.

Нужно реализовать HTTP downloader v2 в пакете assetmgr/downloader.

Требования:
- custom http.Client без общего timeout;
- Transport timeout только на TLS/connect/response headers;
- HEAD preflight;
- GET с Range;
- If-Range через ETag или Last-Modified;
- обработка 200, 206, 416;
- запись только в .part;
- обновление .meta.json;
- idle watchdog: если нет новых байт IdleTimeout, текущая попытка отменяется и запускается retry/resume;
- retry/backoff/jitter;
- progress через ProgressSink;
- context cancel.

Важно:
- не использовать http.DefaultClient для больших файлов;
- не ставить client.Timeout на всё скачивание;
- не писать сразу в final path;
- не требовать sha256 для внешних iiko/syrve;
- не добавлять BITS в этой итерации.

Добавь httptest-based tests:
- download from zero;
- interrupted download -> resume;
- 200 вместо 206 -> restart;
- 416 local complete -> success;
- ETag changed -> restart;
- 500 -> retry;
- 404 -> terminal;
- stalled response -> retry/resume, если это реалистично покрыть тестом.

Запусти:
go test ./...

Итоговый отчёт дай на русском.
```

---

## Итерация 4. FTP downloader v2 и HTTP -> FTP fallback

### Цель

Вынести FTP в новый backend и реализовать source chain для iiko/syrve.

### Задачи

- создать `ftp.go`;
- перенести логику FTP size/RetrFrom/Retr;
- писать только в `.part`;
- проверять итоговый размер;
- реализовать source chain manager;
- реализовать fallback HTTP -> FTP;
- добавить диагностику причины fallback;
- покрыть unit tests/mocks.

### Acceptance criteria

```text
FTP full download работает
FTP resume работает
RetrFrom unsupported -> restart через Retr
HTTP failover на FTP работает
конфликт размера HTTP/FTP даёт понятную ошибку
```

### Промпт для агента

```text
Ты работаешь в репозитории goMH.

Задача: выполнить Итерацию 4 из `ROADMAP_DOWNLOAD_MANAGER.md`.

Нужно реализовать FTP backend v2 и source chain manager.

Сделай:
- assetmgr/downloader/ftp.go;
- FTP download с FileSize, RetrFrom, fallback Retr;
- запись только в .part;
- проверку итогового размера;
- source chain manager, который перебирает DownloadSource по priority;
- HTTP -> FTP fallback для iiko/syrve;
- понятную диагностику fallback reason;
- защиту от логирования FTP-паролей.

Важно:
- если HTTP expected size известен, а FTP remote size отличается, не продолжать молча;
- если FTP remote size совпадает с expected size, можно продолжить существующий .part через RetrFrom;
- не добавлять parallel segmented download;
- не добавлять S3-креды.

Добавь тесты. Если сложно поднять FTP-сервер, выдели FTP client interface и покрой backend моками.

Запусти:
go test ./...

Итоговый отчёт дай на русском.
```

---

## Итерация 5. S3/MinIO manifest для собственных ресурсов

### Цель

Подготовить перенос собственных ресурсов с `f.serty.top` на отдельный S3/MinIO-backed endpoint.

### Важное уточнение

S3 endpoint, bucket, access key и secret key будут переданы агенту отдельно в рабочей сессии. В этом документе и в коде нельзя хардкодить реальные креды.

### Задачи

- добавить поддержку remote asset manifest;
- manifest должен содержать URL, size, sha256, type, destination;
- реализовать overlay manifest поверх базового config;
- собственные ресурсы должны валидироваться через size + sha256;
- не требовать S3-креды на клиентских машинах;
- добавить dev/helper код только если нужен для проверки, без секретов в репозитории.

### Manifest example

```json
{
  "version": 1,
  "generated_at": "2026-05-30T00:00:00Z",
  "assets": {
    "7zip": {
      "url": "https://example-s3-endpoint.example/gomh/assets/7zip/7z.exe",
      "size": 1572864,
      "sha256": "real_sha256_must_be_here",
      "type": "file",
      "destination": "tools/7zip"
    }
  }
}
```

### Acceptance criteria

```text
manifest загружается по HTTPS
assets из manifest добавляются/обновляют AssetCatalog
для manifest assets требуется size + sha256
sha256 mismatch приводит к ошибке и перекачке/отказу
без S3-кредов клиентская утилита может скачать файл по HTTPS URL
```

### Промпт для агента

```text
Ты работаешь в репозитории goMH.

Задача: выполнить Итерацию 5 из `ROADMAP_DOWNLOAD_MANAGER.md`.

Нужно подготовить перенос собственных ресурсов с f.serty.top на отдельный S3/MinIO-backed HTTPS endpoint через remote asset manifest.

Сделай:
- структуру remote manifest;
- загрузку manifest по HTTPS;
- overlay manifest assets поверх базового AssetCatalog;
- обязательную проверку size + sha256 для manifest assets;
- тесты парсинга manifest и overlay;
- тесты sha256 mismatch.

Важно:
- реальные S3 endpoint/creds будут переданы отдельно;
- не хардкодь креды;
- не требуй S3-креды для обычного скачивания клиентской утилитой;
- утилита должна скачивать по обычному HTTPS URL из manifest;
- не добавляй CDN/Cloudflare;
- не добавляй подпись manifest;
- не добавляй историю manifest.

Запусти:
go test ./...

Итоговый отчёт дай на русском.
```

---

## Итерация 6. Windows BITS backend

### Цель

Добавить Windows-only BITS backend как опциональный механизм для HTTP/HTTPS.

### Задачи

- добавить `bits_stub.go` для non-Windows;
- добавить `bits_windows.go` для Windows;
- сначала допустим MVP через PowerShell `Start-BitsTransfer`, если COM-реализация слишком объёмная;
- целевой вариант — COM backend через `go-ole`;
- встроить BITS в общий Backend interface;
- progress каждые 500 ms;
- fallback на Go HTTP, если BITS недоступен;
- не использовать BITS для FTP;
- не использовать BITS для config/manifest.

### Acceptance criteria

```text
Linux/non-Windows сборка не ломается
Windows сборка компилируется
BITS используется только при включённой настройке
BITS failure fallback-ится на Go HTTP
progress прокидывается в ProgressSink
результат всё равно проходит size/sha256 validation
```

### Промпт для агента

```text
Ты работаешь в репозитории goMH.

Задача: выполнить Итерацию 6 из `ROADMAP_DOWNLOAD_MANAGER.md`.

Нужно добавить Windows-only BITS backend.

Сделай:
- bits_stub.go для !windows, который возвращает ErrBackendUnavailable;
- bits_windows.go для windows;
- общий Backend interface, если он ещё не создан;
- выбор BITS только для HTTP/HTTPS source;
- настройку PreferBITSOnWindows/FallbackToGoHTTP;
- fallback на Go HTTP при ошибке BITS;
- progress через ProgressSink;
- обязательную final validation после BITS download.

Если COM-реализация слишком объёмная для одной итерации, допустимо сначала сделать PowerShell Start-BitsTransfer backend, но:
- изолируй его за тем же interface;
- оставь TODO на COM implementation;
- не смешивай PowerShell-вызовы с основной HTTP-логикой.

Важно:
- не использовать BITS для FTP;
- не использовать BITS для manifest/config;
- не писать сразу в final path;
- не логировать секреты.

Запусти:
go test ./...

Если есть Windows-окружение, дополнительно проверь реальную BITS-загрузку на тестовом HTTPS файле.

Итоговый отчёт дай на русском.
```

---

## Итерация 7. Интеграция в assetmgr.Manager и зачистка старого кода

### Цель

Перевести `DownloadToCache` на новую подсистему и удалить/упростить дублирующую старую логику без поломки поведения.

### Задачи

- `assetmgr.Manager.DownloadToCache` должен использовать новый downloader.Manager;
- `ProcessFromCache` должен продолжить работать как раньше;
- старые публичные методы можно оставить как wrappers, если они используются в других местах;
- удалить дублирующий код только после проверки всех usages;
- обновить README/docs;
- добавить manual test checklist.

### Acceptance criteria

```text
все текущие сценарии goMH работают
старый AssetCatalog работает
новый manifest работает
HTTP/FTP/BITS source chain работает
go test ./... проходит
manual checklist заполнен
```

### Промпт для агента

```text
Ты работаешь в репозитории goMH.

Задача: выполнить Итерацию 7 из `ROADMAP_DOWNLOAD_MANAGER.md`.

Нужно интегрировать новую download-manager подсистему в assetmgr.Manager.

Сделай:
- переведи DownloadToCache на новый downloader.Manager;
- сохрани ProcessFromCache без изменения смысла;
- проверь все usages старых DownloadHTTPWithProgress/DownloadFTPWithProgress;
- если методы используются снаружи, оставь wrappers на новую реализацию;
- если не используются, аккуратно удали или пометь deprecated;
- обнови документацию;
- добавь manual test checklist.

Важно:
- не ломай старые config-файлы;
- не ломай существующие меню/команды;
- не меняй поведение распаковки zip без необходимости;
- не добавляй новые большие архитектурные идеи вне roadmap.

Запусти:
go test ./...

Итоговый отчёт дай на русском:
- что было интегрировано;
- какие старые функции остались wrapper-ами;
- какие сценарии проверены;
- какие риски остались.
```

---

## 18. Общий промпт для первого запуска агента

Использовать, если агент начинает с нуля и должен сам определить текущее состояние проекта.

```text
Ты работаешь в репозитории goMH.

Прочитай документ `ROADMAP_DOWNLOAD_MANAGER.md` полностью и используй его как главный источник требований.

Контекст:
- проект уже имеет рабочий assetmgr;
- в assetmgr есть HTTP/FTP скачивание с progress;
- есть поддержка HTTP Range и FTP RetrFrom;
- нужно превратить это в устойчивую download-manager подсистему;
- внешние iiko/syrve HTTP-дистрибутивы не имеют sha256, поэтому для них используем exact size validation;
- собственные ресурсы будут переноситься с f.serty.top на отдельный S3/MinIO-backed HTTPS endpoint и должны проверяться через size + sha256;
- BITS нужен как Windows-only optional backend, но не как единственный механизм;
- запрещено добавлять общий timeout на большое скачивание;
- запрещено делать parallel segmented download;
- запрещено планировать CDN, подпись manifest, историю manifest и зеркалирование iiko/syrve в S3.

Твоя первая задача:
1. Осмотреть текущую структуру проекта.
2. Найти текущие реализации:
   - config.AssetInfo;
   - assetmgr.Manager;
   - DownloadToCache;
   - DownloadHTTPWithProgress;
   - DownloadFTPWithProgress;
   - runtime progress hooks.
3. Составить короткий implementation note в `docs/download-manager/implementation-notes.md`:
   - какие файлы сейчас отвечают за скачивание;
   - какие функции будут перенесены;
   - какие публичные методы нужно сохранить;
   - какие риски обратной совместимости есть.
4. После этого выполнить Итерацию 1 из roadmap.

Важно:
- работай маленькими изменениями;
- не переписывай всё одним коммитом;
- добавляй тесты на каждом этапе;
- после каждого этапа запускай `go test ./...`;
- итоговые отчёты пиши на русском.
```

---

## 19. Промпт для code review агента

```text
Ты выполняешь code review изменений в goMH по документу `ROADMAP_DOWNLOAD_MANAGER.md`.

Проверь PR/изменения на соответствие требованиям:

1. Нет общего timeout на большое скачивание.
2. HTTP downloader использует custom http.Client.
3. Скачивание идёт в .part, а не сразу в final.
4. .meta.json создаётся и используется для resume.
5. Final file появляется только после validation.
6. Для внешних iiko/syrve не требуется sha256.
7. Для собственных S3/MinIO ресурсов требуется size + sha256.
8. HTTP Range/Content-Range обработаны корректно.
9. FTP RetrFrom обработан корректно.
10. HTTP -> FTP fallback не игнорирует конфликт размера.
11. Retry/backoff/jitter реализованы.
12. Idle watchdog есть.
13. BITS изолирован build tags и не ломает non-Windows сборку.
14. BITS имеет fallback на Go HTTP.
15. Секреты не логируются.
16. Старые конфиги остаются совместимыми.
17. go test ./... проходит.
18. Нет реализации исключённых решений:
    - CDN;
    - manifest signing;
    - parallel segmented download;
    - зеркалирование iiko/syrve в S3;
    - история manifest.

Дай review на русском:
- blocking issues;
- non-blocking issues;
- что реализовано хорошо;
- какие тесты стоит добавить;
- можно ли принимать PR.
```

---

## 20. Промпт для тестового агента

```text
Ты тестовый агент в репозитории goMH.

Твоя задача — проверить download-manager изменения по `ROADMAP_DOWNLOAD_MANAGER.md`.

Сделай:
1. Запусти `go test ./...`.
2. Найди и запусти все tests, связанные с downloader/assetmgr/config.
3. Проверь ручные сценарии, если окружение позволяет:
   - HTTP download;
   - HTTP resume;
   - HTTP interruption;
   - FTP fallback;
   - damaged .part;
   - damaged final;
   - S3/MinIO manifest asset with sha256;
   - Windows BITS, если доступна Windows.
4. Проверь, что в логах нет FTP password, S3 secret, presigned query string.
5. Проверь, что большие файлы не используют общий timeout.

Сформируй отчёт на русском:
- окружение;
- команды;
- результаты;
- найденные ошибки;
- рекомендации;
- можно ли считать итерацию готовой.
```

---

## 21. Минимальная последовательность внедрения

Если нужно ускорить реализацию, минимальный безопасный порядок такой:

```text
1. Domain model + config compatibility.
2. .part/.meta + validation.
3. HTTP downloader v2.
4. FTP backend + source fallback.
5. S3/MinIO manifest.
6. BITS backend.
7. Полная интеграция и зачистка старого кода.
```

Нельзя начинать с BITS до появления общей модели `DownloadJob`, `ProgressSink`, validation и finalization, иначе BITS станет отдельной неподдерживаемой веткой.

---

## 22. Финальная целевая формула

Для внешних iiko/syrve:

```text
Надёжность = HTTP Range + FTP RetrFrom fallback + retry/backoff + idle watchdog + .part/.meta + exact size validation.
```

Для собственных ресурсов:

```text
Надёжность = S3/MinIO-backed HTTPS + retry/backoff + idle watchdog + .part/.meta + exact size + SHA256.
```

Для Windows:

```text
BITS = optional native backend через общий интерфейс, с fallback на Go HTTP.
```

Главный принцип: финальный файл считается пригодным к использованию только после прохождения выбранной политики validation.
