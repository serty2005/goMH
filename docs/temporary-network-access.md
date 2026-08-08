# Временный доступ к подсети устройства

## Модель безопасности

Операция создаёт только одну transient-запись IPv4 через `CreateUnicastIpAddressEntry`. Она не вызывает DHCP/static-команды, не изменяет DNS или gateway и не перезапускает сетевой адаптер.

Порядок операции:

```text
validate → transaction PREPARED → watchdog armed → CreateUnicastIpAddressEntry
→ DAD Preferred → GetBestRoute2 verification → ACTIVE → exact cleanup → COMPLETE
```

Transaction хранится в `<root_path>\network-transactions`. До системной мутации в ней записаны LUID/GUID интерфейса, IP, `/24`, target, TTL и watchdog. После создания сохраняется `CreationTimeStamp` NetIO-записи. Cleanup сверяет LUID, точный IP, prefix и timestamp. Если принадлежность записи доказать нельзя, адрес не удаляется; конечный `ValidLifetime` остаётся дополнительной страховкой.

Watchdog — one-shot задача `goMH_TemporaryIPv4_<id>` с запуском от SYSTEM в момент expiry. Она вызывает только внутренний флаг `-internal-network-temp-cleanup` и не открывает TUI или публичную automation-операцию. Отсутствующий адрес и повторный cleanup считаются успешными.

## Диагностика

- transaction: `<root_path>\network-transactions\<id>.json`;
- watchdog: Планировщик заданий, имя `goMH_TemporaryIPv4_<id>`;
- основной лог goMH содержит `transaction_id`, LUID/alias, target, временный IP, DAD, route verification и cleanup reason/result.

После успешного удаления адреса transaction JSON удаляется. Если в каталоге больше нет активных или аварийных транзакций, удаляется и сам каталог `network-transactions`. При ошибке cleanup JSON сохраняется для безопасного повторного восстановления и диагностики; подробная история также остаётся в основном логе goMH.

## Фоновая работа

Выход по `Esc` или пункт «Оставить работать в фоне» возвращает в главное меню без удаления адреса и watchdog. После этого можно запустить сканирование сети или другой модуль. При повторном входе в «Временный доступ к подсети устройства» активный сеанс отображается первым: goMH повторно проверяет точный IPv4, `CreationTimeStamp`, маршрут и доступность устройства, а затем открывает тот же экран управления.

Чтобы удалить адрес раньше TTL, нужно вернуться к активному сеансу и выбрать «Завершить временный доступ». Независимо от UI адрес остаётся ограничен lifetime Windows и задачей watchdog.

## Ручная проверка на тестовой Windows 10/11

Все проверки выполнять на тестовой машине с локальным доступом или резервным каналом управления.

### A. DHCP Ethernet

1. Зафиксировать `Get-NetIPConfiguration` и `Get-DnsClientServerAddress`.
2. Создать временный доступ к устройству в другой `/24`.
3. Убедиться, что DHCP, основной IPv4, gateway и DNS не изменились, а secondary IPv4 появился.
4. Проверить connected route, устройство и AnyDesk/TeamViewer.
5. Завершить сеанс и убедиться, что исчез только temporary IPv4.

### B. Аварийное закрытие

1. Создать temporary session с коротким тестовым TTL.
2. Выполнить `taskkill /F /IM goMH.exe`.
3. После expiry проверить удаление адреса и завершённое состояние transaction.

### C. Перезагрузка

1. Создать temporary session и перезагрузить Windows.
2. Проверить отсутствие temporary IPv4 и нормальную работу DHCP.
3. Запустить goMH и проверить безопасное закрытие stale transaction.

### D. Duplicate candidate

1. Занять предлагаемый IP другим узлом.
2. Проверить DAD Duplicate, точечное удаление кандидата и переход к следующему адресу.

### E. Несколько NIC

1. Оставить активными Ethernet, Wi-Fi и VPN.
2. Проверить, что goMH показывает выбор и не выбирает VPN молча.

### F. ICMP заблокирован

1. Запретить ping на target, оставив доступным HTTP или TCP 9100.
2. Проверить, что route/session остаются успешными и TCP отображается как доступный probe.
