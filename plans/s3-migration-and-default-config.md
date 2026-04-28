# План: миграция goMH на Yandex S3 и отказ от статичных манифестов

Дата старта: 2026-04-02
Статус: в работе
Владелец: агент Codex / команда goMH

## Как вести этот файл

- После завершения шага менять `[ ]` на `[x]`.
- После каждого заметного изменения обновлять разделы `Текущее состояние`, `Следующий шаг`, `Журнал`.
- Если по ходу работ меняется решение, не стирать старое молча: кратко зафиксировать, что поменяли и почему.
- Не считать задачу завершенной, пока не обновлены тесты, документация и этот файл.

## Цель

Перевести goMH с хранения артефактов на нашем сервере на Yandex S3, убрать зависимость от статичных манифестов на нашем сервере, заменить получение версий Syrve на live-чтение с Syrve FTP по FTPS + SSL/TLS, а также ввести генерацию безопасного конфигурационного профиля по умолчанию, если конфиг не найден локально и на удаленном хранилище.

## Зафиксированные требования

- Больше не использовать статичные манифесты с нашего сервера.
- Исключение: допустимо сохранить только версионность/compatibility-слой для плагинов iiko.
- Версии Syrve больше не брать из `syrve-manifest.json`; читать их с Syrve FTP по FTPS + SSL/TLS.
- Логин/пароль Syrve FTP хранить в коде, по аналогии с iiko FTP.
- Эти креды не должны попадать в `config.json`, UI, логи или пользовательский конфиг.
- Если рядом с программой нет конфига и на удаленном хранилище он тоже не найден, нужно сгенерировать default-config.
- Generated default-config не должен включать проприетарные модули/ассеты:
  - брендовый TeamViewer
  - POSRelayd
  - принудительную работу из `C:\MH`
- В generated default-config рабочая root-папка должна жить во временной папке пользователя и удаляться после завершения программы.
- При таком режиме рабочей директорией считается место запуска программы, а не `C:\MH`.

## Подтвержденное текущее состояние

- [x] Найден текущий bootstrap конфига в [main.go](/c:/safe/repos/goMH/main.go): если `config.json` рядом отсутствует, он скачивается с `http://f.serty.top/distr/installer/config.json`.
- [x] Найдена дублирующая загрузка конфига в [config/config.go](/c:/safe/repos/goMH/config/config.go) и [config/load_quiet.go](/c:/safe/repos/goMH/config/load_quiet.go).
- [x] Найдено получение версий Syrve через статичный манифест в [modules/distro/syrve.go](/c:/safe/repos/goMH/modules/distro/syrve.go).
- [x] Найден статичный manifest плагинов iiko в [modules/iiko-plugins/iiko-plugins.go](/c:/safe/repos/goMH/modules/iiko-plugins/iiko-plugins.go).
- [x] Подтверждено, что список live-плагинов уже частично строится динамически из Rapid и FTP в [modules/iiko-plugins/live_sources.go](/c:/safe/repos/goMH/modules/iiko-plugins/live_sources.go).
- [x] Подтверждено, что текущий FTP-клиент `github.com/jlaffaye/ftp` уже поддерживает `DialWithExplicitTLS`, значит FTPS можно внедрить без обязательной замены библиотеки.
- [x] Найдено, что portable-версии сейчас завязаны на наш FTP/HTTP и live-листинг каталога, значит миграция на S3 затронет не только `asset_catalog`, но и discovery portable-архивов.
- [x] Подтверждено, что `assetmgr` сейчас понимает только HTTP/FTP загрузку и опирается на `AssetCatalog` из конфига.
- [x] Подтверждено, что `RootPath` и `AssetsCachePath` широко используются по проекту и сейчас по умолчанию ориентированы на `C:\MH`.

## Целевая архитектура

### 1. Bootstrap конфигурации

Порядок поиска конфига:

1. Явно переданный `--config`.
2. `config.json` рядом с exe.
3. Удаленный конфиг в S3.
4. Сгенерированный safe default-config.

Важное правило:

- отсутствие удаленного конфига не должно быть ошибкой запуска, если можно безопасно собрать generated default-config.

### 2. Хранилище

Новый внешний storage должен быть абстрагирован так, чтобы бизнес-логика не знала, лежит объект на `f.serty.top` или в Yandex S3.

Практическое решение для первого этапа:

- основным публичным транспортом считать HTTPS-объекты из S3;
- если для listing/exists не хватит простого HTTP, добавить S3-aware слой, но не размазывать его по модулям;
- все старые URL `f.serty.top` постепенно заменить на S3 endpoint/prefix.

### 3. Live-источники вместо статичных манифестов

- Syrve версии: FTPS directory listing.
- Portable-сборки: live-listing префиксов/каталогов в S3 либо другой live-source без JSON manifest.
- iiko plugins:
  - оставить только compatibility/version matrix;
  - список и версии плагинов должны оставаться live.

### 4. Generated default-config

Generated default-config должен включать только безопасный минимальный профиль:

- без брендового TeamViewer;
- без POSRelayd;
- без жесткой root-папки `C:\MH`;
- с temp-root внутри `%TEMP%`;
- с cleanup temp-root при штатном выходе и при обработке сигналов;
- с таким набором модулей и ассетов, который не требует проприетарной инфраструктуры.

## План реализации

### Этап 0. Подготовка и фиксация контракта

- [x] Снять текущее состояние по всем точкам зависимости от нашего сервера.
- [ ] Зафиксировать в коде и/или документе единые константы удаленного storage:
  - базовый S3 endpoint
  - ключ/путь до remote config
  - префиксы для assets/self-update/portable
- [ ] Определить, будет ли S3 bucket публичным по HTTPS или потребуется доступ с ключами.
- [ ] Зафиксировать, какие модули допустимы в generated default-config, а какие должны быть исключены полностью.

### Этап 1. Рефакторинг bootstrap конфига

- [ ] Вынести логику поиска и подготовки конфига из [main.go](/c:/safe/repos/goMH/main.go) в отдельный слой, например `config/bootstrap.go`.
- [ ] Объединить обычную и quiet-загрузку вокруг одного внутреннего загрузчика, чтобы не дублировать HTTP/file JSON parsing.
- [ ] Реализовать единый `ResolveConfig(...)`, который возвращает:
  - итоговый `*config.Config`
  - источник конфига (`flag`, `local`, `s3`, `generated-default`)
  - cleanup-функцию, если был создан temp-файл или temp-root
- [ ] Если удаленный конфиг отсутствует или недоступен, переходить к генерации default-config, а не падать.
- [ ] Покрыть bootstrap тестами:
  - local config найден
  - local не найден, S3 найден
  - local и S3 не найдены, создан default-config
  - explicit `--config` имеет приоритет

### Этап 2. Generated default-config и временный runtime-профиль

- [ ] Добавить фабрику `config.NewGeneratedDefault(...)`.
- [ ] В generated default-config вычислять temp-root через `os.MkdirTemp(os.TempDir(), "goMH-*")`.
- [ ] `RootPath` выставлять во временный каталог, а `AssetsCachePath` внутрь этого же temp-root.
- [ ] Исключить из generated default-config проприетарные сущности:
  - TeamViewer branded config
  - POSRelayd assets/config
  - любые asset URL, указывающие на приватные объекты без доступного публичного replacement
- [ ] Исключить создание/ожидание `C:\MH` в generated default-config.
- [ ] Проверить места, где код молча предполагает `C:\MH`, и перевести их на `cfg.RootPath`.
- [ ] Обеспечить cleanup временного root-path при завершении процесса.
- [ ] Учесть сценарии resume/reboot:
  - generated default-config не должен пытаться создавать долгоживущие resume-задачи, если это ломает cleanup;
  - при необходимости явно отключить resume для ephemeral-режима.

### Этап 3. Переход с нашего storage на S3

- [ ] Убрать прямые hardcoded URL `f.serty.top` из кода там, где это возможно.
- [ ] Перенести ссылки self-update на S3:
  - [modules/selfupdate/updater.go](/c:/safe/repos/goMH/modules/selfupdate/updater.go)
  - текущий `self_update_config` в конфиге/генераторе
- [ ] Перенести `asset_catalog` на S3 object URLs или другой единый remote source.
- [ ] Обновить [config.json](/c:/safe/repos/goMH/config.json) как пример штатного профиля под S3.
- [ ] Обновить [README.md](/c:/safe/repos/goMH/README.md), чтобы там больше не фигурировал `f.serty.top` как основной источник.
- [ ] При необходимости расширить [assetmgr/manager.go](/c:/safe/repos/goMH/assetmgr/manager.go):
  - либо поддержкой `S3`
  - либо оставить `HTTP`, но полностью заменить URL на S3 HTTPS
- [ ] Не хранить S3-секреты в пользовательском конфиге.

### Этап 4. Удаление статичных manifest-зависимостей

- [ ] Заменить чтение `syrve-manifest.json` в [modules/distro/syrve.go](/c:/safe/repos/goMH/modules/distro/syrve.go) на live-чтение версий с Syrve FTP.
- [ ] Удалить зависимость от статичного списка версий Syrve на нашем сервере.
- [ ] Пересобрать plugin-manifest для iiko так, чтобы он содержал только compatibility/version metadata.
- [ ] Удалить из plugin-manifest необходимость хранить список/версии плагинов как статичную серверную истину.
- [ ] Проверить, не осталось ли в проекте других manifest/json/xml, играющих роль статичного серверного индекса.

### Этап 5. Syrve FTPS + SSL/TLS

- [ ] Добавить в код hardcoded-константы для Syrve FTP host/user/pass по аналогии с iiko FTP.
- [ ] Не добавлять эти креды в [config/config.go](/c:/safe/repos/goMH/config/config.go) и в `config.json`.
- [ ] Реализовать отдельный dial/helper для Syrve FTPS на основе `ftp.DialWithExplicitTLS(...)`.
- [ ] Ввести безопасный `tls.Config` для FTPS.
- [ ] Реализовать live-listing каталога релизов Syrve и выделение версий через regex.
- [ ] Нормализовать сортировку версий Syrve тем же способом, что и для iiko.
- [ ] Не логировать креды даже в debug.
- [ ] Добавить тесты на парсинг имен версий и fallback-обработку ошибок.

### Этап 6. Portable discovery без статичных манифестов

- [ ] Убрать зависимость portable discovery от нашего FTP как единственного источника листинга.
- [ ] Для portable архивов реализовать live-listing по S3 prefix/bucket listing или эквивалентному источнику.
- [ ] Для iiko/Syrve portable сохранить текущую схему шаблонов имен архивов, но источник списка версий сделать live.
- [ ] Проверить [modules/distro/iiko.go](/c:/safe/repos/goMH/modules/distro/iiko.go) и [modules/distro/syrve.go](/c:/safe/repos/goMH/modules/distro/syrve.go) на смешение `http_source` и `ftp_source`.
- [ ] Убрать неявное предположение, что даже HTTP-source обязан листиться через FTP.

### Этап 7. Безопасность и сокрытие закрытых данных

- [ ] Проверить, что hardcoded FTP-креды нигде не сериализуются обратно в JSON.
- [ ] Проверить, что креды не попадают в `slog.Info/Debug/Warn/Error`.
- [ ] Проверить, что generated default-config не раскрывает внутренние endpoint/секреты при сохранении или дампе.
- [ ] Если будет введен S3 клиент с ключами, хранить ключи только в коде/secure runtime constants, но не в пользовательском конфиге.

### Этап 8. Тесты и приемка

- [ ] Добавить unit-тесты на bootstrap/fallback generated default-config.
- [ ] Добавить unit-тесты на Syrve version parsing и FTPS helper.
- [ ] Добавить unit-тесты на generated default-config:
  - нет `C:\MH`
  - нет TeamViewer/POSRelayd
  - temp-root создается и удаляется
- [ ] Добавить smoke-тесты на portable discovery provider.
- [ ] Провести grep-проверку репозитория на остатки `f.serty.top`, `syrve-manifest.json`, `plugins-manifest.json`.
- [ ] Обновить README и этот план после завершения миграции.

## Порядок внедрения по коммитам

Рекомендуемая нарезка:

1. bootstrap config + generated default-config без изменения бизнес-модулей;
2. storage/S3 migration для self-update и asset URLs;
3. Syrve FTPS live-source;
4. portable discovery live-source;
5. cleanup plugin manifest contract;
6. тесты, документация, зачистка legacy URL.

## Критерии готовности

- Программа стартует без локального конфига и без remote config, создавая безопасный generated default-config.
- В generated default-config не создается и не требуется `C:\MH`.
- Версии Syrve получаются live по FTPS, без `syrve-manifest.json`.
- В коде не осталось обязательной зависимости от статичных манифестов нашего сервера.
- Для iiko plugins статичным может остаться только compatibility/version mapping.
- Основные assets и self-update смотрят в Yandex S3, а не на наш сервер.
- Пользователь не видит и не может прочитать закрытые FTP/S3 креды из конфига.

## Риски и контрольные точки

- Риск: слишком много мест завязано на `cfg.RootPath` как на постоянный каталог.
  - Контроль: отдельно проверить resume, backup, logs, temp, plugins backup.
- Риск: portable discovery сейчас логически смешивает HTTP download и FTP listing.
  - Контроль: сначала выделить provider discovery, потом менять transport.
- Риск: generated default-config может случайно оставить модуль, который тянет приватный asset.
  - Контроль: перед включением каждого модуля проверить его обязательные asset/config зависимости.
- Риск: FTPS сервер Syrve может требовать особые TLS-параметры.
  - Контроль: реализовать helper изолированно и покрыть логикой graceful fallback/error reporting.

## Следующий шаг

Сделать первый кодовый этап: вынести bootstrap конфига в отдельный слой и добавить генерацию safe default-config с временным root-path и cleanup.

## Журнал

- 2026-04-02: выполнен первичный аудит проекта, подтверждены точки входа для migration bootstrap, Syrve manifest, plugin manifest и зависимости от `f.serty.top`.
