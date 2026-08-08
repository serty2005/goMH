# Подробное руководство goMH

Этот документ дополняет основной [README](../README.md). Здесь собраны сведения для инженера, который сопровождает конфигурацию, понимает состав модулей и использует служебные режимы запуска.

## Режимы запуска

Обычный запуск:

```powershell
.\goMH.exe
```

Запуск с явным конфигом:

```powershell
.\goMH.exe -config .\config.json
```

Запуск с ключом `-gui`:

```powershell
.\goMH.exe -gui
```

Служебные режимы возобновления используются самой утилитой после сценариев, где может потребоваться перезагрузка:

```powershell
.\goMH.exe -module iiko -resume .\path\to\resume.json
.\goMH.exe -module Regime -resume .\path\to\resume.json
```

Оператору вручную запускать режимы `-module ... -resume ...` обычно не требуется.

## Как выбирается config.json

При обычном запуске goMH выбирает конфигурацию в таком порядке:

1. Если указан ключ `-config`, используется переданный путь или URL.
2. Если ключ `-config` не указан, программа ищет `config.json` рядом с `goMH.exe`.
3. Если локального `config.json` нет, программа скачивает конфиг с адреса по умолчанию:

```text
http://f.serty.top/distr/installer/config.json
```

Удаленный конфиг сохраняется во временный файл в папке `temp`. После завершения работы временные файлы утилиты очищаются, кроме случаев, когда нужно сохранить данные для возобновления после перезагрузки.

`config.json` является основным способом менять поведение утилиты без пересборки: состав меню, ссылки на дистрибутивы, параметры установщиков, источники portable-версий, правила сбора логов, драйверы и служебные URL.

## Общая структура config.json

Ниже приведен сокращенный пример. Он показывает форму файла, но не должен использоваться как готовый рабочий конфиг.

```json
{
  "root_path": "C:\\MH",
  "assets_cache_path": "C:\\MH\\_assets",
  "logging": {
    "level": "INFO",
    "file_enabled": true
  },
  "self_update_config": {
    "enabled": true,
    "reference_url": "https://example.invalid/goMH.exe",
    "hash_url": "https://example.invalid/goMH.md5",
    "reference_url_x86": "https://example.invalid/goMH-x86.exe",
    "hash_url_x86": "https://example.invalid/goMH-x86.md5",
    "temp_dir": "C:\\MH\\temp"
  },
  "modules": [
    { "id": "iiko" },
    { "id": "iiko-plugins" },
    { "id": "RemoteAccess" },
    { "id": "VComCaster" },
    { "id": "FRPC" },
    { "id": "Regime" },
    { "id": "FiscalDrivers" },
    { "id": "UTM" },
    { "id": "ServiceUtils" }
  ],
  "ftp_config": [
    {
      "host": "ftp.example.invalid",
      "port": 21,
      "user": "user",
      "pass": "password"
    }
  ],
  "asset_catalog": {
    "example_asset": {
      "url": "https://example.invalid/file.zip",
      "type": "zip",
      "destination": "example",
      "download_method": "HTTP"
    }
  }
}
```

### Корневые пути

- `root_path` - основная рабочая папка утилиты. Обычно используется `C:\MH`.
- `assets_cache_path` - кеш скачанных установщиков, архивов и вспомогательных файлов.

### Логирование

Актуальный раздел:

```json
"logging": {
  "level": "INFO",
  "file_enabled": true
}
```

- `level` задает уровень подробности: обычно `INFO`, для диагностики можно использовать более подробный уровень, если он поддержан текущей сборкой.
- `file_enabled` включает запись общего лога рядом с исполняемым файлом.
- Старое поле `log_level` продолжает читаться как совместимый алиас для `logging.level`.

### Самообновление

Раздел `self_update_config` управляет проверкой новой версии при старте:

- `enabled` включает или отключает проверку;
- `reference_url` и `hash_url` указывают основной исполняемый файл и его MD5;
- `reference_url_x86` и `hash_url_x86` могут использоваться для 32-битной сборки;
- `temp_dir` задает папку для временных файлов обновления.

Если обновление найдено, goMH скачивает новый EXE, заменяет текущий файл и перезапускается. В режиме возобновления после перезагрузки проверка обновлений пропускается.

### Список модулей

Раздел `modules` определяет, какие пункты попадут в меню и в каком порядке. Актуальные идентификаторы:

| ID | Назначение |
| --- | --- |
| `iiko` | Дистрибутивы iiko/Syrve и связанные сценарии установки |
| `iiko-plugins` | Установка плагинов iikoFront |
| `RemoteAccess` | Удаленный доступ: TeamViewer, LiteManager, POSRelayd |
| `VComCaster` | Настройка сканеров штрих-кодов через VComCaster |
| `FRPC` | Проброс портов через FRPC |
| `Regime` | Установка локального компонента Regime |
| `FiscalDrivers` | Установка драйверов ККТ |
| `UTM` | Установка УТМ |
| `ServiceUtils` | Сбор логов, очистка, OrderCheck, FrontTools, автозапуск |

Если ID отсутствует в `modules`, соответствующий пункт не показывается в основном меню.

## Дистрибутивы iiko/Syrve

Раздел `distro_config` описывает компоненты iiko и Syrve, portable-источники, патчи и правила работы с плагинами.

Пример компонента:

```json
{
  "id": "iiko_front",
  "menu_text": "iikoFront",
  "install_args": "/install /passive",
  "run_after": "C:\\Program Files\\iiko\\iikoRMS\\Front.Net\\iikoFront.Net.exe",
  "url_template": "https://downloads.example.invalid/{{VERSION}}/Setup.Front.exe",
  "portable_archive_key": ""
}
```

Поля компонента:

- `id` - стабильный идентификатор компонента. Используется также в automation-запросах.
- `menu_text` - название в меню.
- `install_args` - аргументы запуска установщика.
- `run_after` - путь к программе, которую можно запустить после установки.
- `url_template` - ссылка на установщик. Если в ссылке есть `{{VERSION}}`, goMH подставляет выбранную версию.
- `portable_archive_key` - ключ для поиска portable-архива в источниках.

Для каждого бренда есть отдельные списки:

- `distro_config.iiko.components`;
- `distro_config.syrve.components`.

Для iiko также может быть задан `patches_base_url`, откуда берется список патчей.

Portable-источники задаются отдельно:

- `iiko_portable_sources`;
- `syrve_portable_sources`.

Каждый portable-источник может иметь HTTP- и FTP-вариант. В `archive_names` указывается соответствие ключа архива и шаблона имени файла.

Дополнительные списки:

- `excluded_plugins` - плагины, которые нужно игнорировать при подборе;
- `auto_update_plugins` - фрагменты имен плагинов, которые можно обновлять автоматически после установки.

## Каталог ресурсов

`asset_catalog` связывает внутренний ID ресурса с местом загрузки.

```json
"asset_catalog": {
  "7zip_installer": {
    "url": "https://example.invalid/7z.exe",
    "type": "file",
    "destination": "",
    "download_method": "HTTP"
  },
  "VComCaster_Package": {
    "url": "https://example.invalid/vcomcaster.zip",
    "type": "zip",
    "destination": "vcomcaster",
    "download_method": "HTTP"
  }
}
```

Поля ресурса:

- `url` - HTTP/HTTPS/FTP-адрес или источник, который умеет обработать менеджер ресурсов;
- `type` - тип ресурса, например `file` или `zip`;
- `destination` - подпапка для распаковки или размещения;
- `download_method` - метод загрузки, если его нужно указать явно.

Модули обращаются к ресурсам по ID, поэтому при изменении ключей важно обновлять соответствующие настройки модулей.

## FRPC

Модуль использует [FRPC](https://github.com/fatedier/frp) для проброса портов.

```json
"frpc_config": {
  "install_path": "C:\\MH\\FRPC",
  "service_name": "FRPC",
  "port_range": "50500-50600",
  "frpc_download_url": "https://example.invalid/frpc.zip",
  "nssm_download_url": "https://example.invalid/nssm.zip",
  "server_config": {
    "host": "frps.example.invalid",
    "api_port": 443,
    "tunnel_port": 50005,
    "user": "user",
    "pass": "password"
  }
}
```

Назначение полей:

- `install_path` - папка установки клиента;
- `service_name` - имя службы Windows;
- `port_range` - диапазон портов, в котором goMH ищет свободный порт;
- `frpc_download_url` - архив клиента FRPC;
- `nssm_download_url` - архив NSSM для установки службы;
- `server_config` - параметры FRPS/API-сервера.

## VComCaster

Модуль использует [VComCaster](https://github.com/symp1ex/VComCaster) для настройки сканеров штрих-кодов в режиме COM-порта.

Обычно связанные ресурсы задаются в `asset_catalog`: пакет VComCaster, com0com и вспомогательные архивы. При выполнении сценария goMH скачивает ресурсы, ищет подключенный сканер, готовит конфигурацию и может внести изменения в настройки iikoFront, если это безопасно для текущего состояния процесса.

## Удаленный доступ

Раздел `TeamViewerConfig`:

```json
"TeamViewerConfig": {
  "ShortURL": "https://get.teamviewer.com/example",
  "ApiURL": "https://get.teamviewer.com/api/CustomDesign"
}
```

- `ShortURL` - короткая ссылка на дистрибутив TeamViewer Host;
- `ApiURL` - API-адрес Custom Design.

LiteManager и POSRelayd обычно описываются через ресурсы в `asset_catalog`.

## Утилиты обслуживания

Раздел `MaintenanceConfig`:

```json
"MaintenanceConfig": {
  "TempPaths": [
    "C:\\Windows\\Temp",
    "C:\\Users\\*\\AppData\\Local\\Temp"
  ],
  "LogCollectorPaths": [
    "C:\\Users\\*\\AppData\\Roaming\\iiko"
  ],
  "7zipAssetID": "7zip_installer"
}
```

- `TempPaths` - пути для очистки временных файлов. Поддерживаются маски с `*`.
- `LogCollectorPaths` - каталоги, откуда собираются логи.
- `7zipAssetID` - ресурс 7-Zip из `asset_catalog`, если он нужен для обработки архивов.

Утилиты обслуживания включают:

- очистку временных файлов;
- сбор логов за выбранное количество дней;
- просмотр файла лога в реальном времени;
- запуск OrderCheck;
- запуск FrontTools;
- управление автозапуском.
- диагностику сети и сканирование локальных подсетей;
- временный доступ к подсети принтера или другого сетевого устройства.

### Временный доступ к подсети устройства

Функция находится в `Утилиты обслуживания` → `Диагностика сети` → `Временный доступ к подсети устройства`.

Введите только текущий IPv4 устройства. goMH считает, что устройство использует `/24`, и предлагает временный адрес по правилу: сначала адрес устройства + 1 и далее до `.254`, затем от `.1` до адреса устройства. Предложение можно отредактировать в пределах той же `/24`. Окончательную проверку занятости выполняет Windows Duplicate Address Detection; при конфликте goMH пробует следующий кандидат.

goMH добавляет отдельный непостоянный IPv4 через Windows NetIO API. Основной DHCP-адрес, DNS, default gateway и состояние адаптера не изменяются. Перед добавлением адреса создаётся watchdog в Планировщике заданий. По умолчанию адрес и watchdog действуют 30 минут; активный сеанс можно продлить ещё на 30 минут.

В экране активного сеанса доступны повторная проверка устройства, открытие HTTP и штатное завершение. Экран показывает точное время автоматического удаления временного IP. При завершении удаляется только NetIO-запись, созданная конкретной transaction. Если goMH аварийно закрыт, watchdog выполняет тот же идемпотентный cleanup. После перезагрузки transient-адрес уже отсутствует, а startup recovery закрывает устаревшую transaction без восстановления DHCP.

Файлы состояния находятся в `<root_path>\network-transactions`. Они содержат идентификатор transaction, интерфейс, временный IP, TTL, DAD и результат cleanup. Задачи watchdog называются `goMH_TemporaryIPv4_<transaction-id>`. Подробная модель безопасности и checklist ручной проверки описаны в [temporary-network-access.md](temporary-network-access.md).

## Фискальные драйверы и УТМ

Драйверы ККТ задаются в `fiscal_drivers_config`:

```json
"fiscal_drivers_config": [
  {
    "id": "atol",
    "menu_text": "АТОЛ ДТО",
    "asset_id": "atol_driver",
    "install_args": "/quiet"
  }
]
```

- `id` - идентификатор драйвера;
- `menu_text` - пункт меню;
- `asset_id` - ресурс установщика из `asset_catalog`;
- `install_args` - аргументы запуска установщика.

УТМ задается в `utm_config`:

```json
"utm_config": {
  "menu_text": "Установка УТМ",
  "asset_id": "utm_installer",
  "install_args": "/S"
}
```

## Automation-режим

`automation` - скрытый non-interactive режим для внешних оркестраторов и интеграций. Он не предназначен для ручной работы оператора.

Список операций:

```powershell
.\goMH.exe automation list-operations
```

Запуск из файла:

```powershell
.\goMH.exe automation run --config .\config.json --request .\request.json
```

Запуск через stdin:

```powershell
Get-Content .\request.json | .\goMH.exe automation run --stdin
```

Контракт:

- `stdout` содержит только финальный JSON response;
- `stderr` содержит диагностические JSON-lines события;
- версия контракта: `gomh.automation/v1`;
- request должен содержать `request_id` и `operation_id` либо пару `module` + `action`;
- `dry_run=true` строит и проверяет план без выполнения операции;
- `timeout_seconds` ограничивает время выполнения;
- `root_override` временно переопределяет `root_path`;
- `log_options.include_entries=true` добавляет task-log в ответ.

Текущие зарегистрированные операции:

| Operation ID | Статус | Назначение |
| --- | --- | --- |
| `serviceutils.clean_temp` | поддерживается | Очистка временных файлов по правилам конфигурации |
| `serviceutils.collect_logs` | поддерживается | Сбор логов в архив за заданный период |
| `distro.install_component` | поддерживается | Установка компонента iiko/Syrve |
| `serviceutils.view_log` | не поддерживается | Требует интерактивного или долгоживущего сценария |

Минимальный request для сбора логов:

```json
{
  "contract_version": "gomh.automation/v1",
  "request_id": "req-logs-001",
  "operation_id": "serviceutils.collect_logs",
  "parameters": {
    "log_days": 7
  }
}
```

Request для установки компонента:

```json
{
  "contract_version": "gomh.automation/v1",
  "request_id": "req-install-001",
  "operation_id": "distro.install_component",
  "timeout_seconds": 1800,
  "parameters": {
    "brand": "iiko",
    "component_id": "iiko_front",
    "version": "8.9.7012.0",
    "run_auto_update_plugins": true
  }
}
```

Exit codes:

| Код | Значение |
| --- | --- |
| `0` | Успех |
| `2` | Невалидный request или аргументы CLI |
| `3` | Неизвестная операция |
| `4` | Операция известна, но не поддерживается в automation-режиме |
| `5` | Ошибка выполнения |
| `6` | Timeout |
| `7` | Внутренняя ошибка automation runtime |

Более подробный контракт находится в [automation-cli.md](automation-cli.md).

## Практические правила сопровождения

- Не меняйте рабочий `config.json` без понимания, какие модули используют конкретный ключ.
- Не публикуйте реальные FTP/API-учетные данные.
- Для новых ссылок сначала проверяйте доступность файла вручную.
- Для ресурсов в `asset_catalog` сохраняйте стабильные ID: на них ссылаются модули.
- После изменения списка `modules` проверьте запуск меню.
- После изменения `distro_config` проверьте, что выбранный компонент отображается и строит корректную ссылку.
- После изменения automation-сценариев проверьте `automation list-operations` и `dry_run`.
