Дополнительно: как собрать и использовать проект

Требования
- Go 1.22 (указывает `go.mod`).

Сборка и запуск
- Отформатируйте исходники и соберите бинарник из корня репозитория:

```powershell
gofmt -w .
go build
```
- Запуск сервера (в корне репозитория):

```powershell
go run .
```

По умолчанию сервер слушает на :4040 (см. `main.go`). Время чтения заголовка — 5s, время записи — 600s (для длительных генераций/рендеринга).

HTTP API (подробно)
- `GET`/`POST /generate`
  - Основной endpoint для создания новой генерации.
  - Параметры можно передать как query-строкой, так и JSON телом в соответствии с `GenerateParams` (`types.go`).
- `GET /tx/{id}/info`
  - Возвращает полную структуру `Transaction` (включая `Provenance.PerHTTPSeeds` для `http`/`mix` режимов).

- `GET /tx/{id}/trng?n=<N>&format=hex|bytes`
  - Восстанавливает TRNG из `Transaction.Seed` и `Provenance` и отдаёт N байт в выбранном формате.

- `GET /tx/{id}/stats`
  - Возвращает `SimulationData`/статистику/результаты симуляции.

- `GET /tx/{id}/verify`
  - Выполняет набор проверок (chain_valid, tx_found, data_hash_match, bits_hash_match, published_in_chain) и возвращает их в JSON.

- Дополнительные endpoints:
  - `GET /txs` — список транзакций (краткая информация).
  - `GET /chain` — просмотра цепочки блоков.
- `POST /stats/upload` — загрузка внешней статистики (используется в инструментах).

Entropy modes — детали (из `entropy.go`)
- `repro` — строго детерминированный режим: используйте `seed64` чтобы задать мастер-сид. Подходящ для тестов и воспроизводимости.
- `os` — системный крипто-PRNG (non-reproducible).
- `jitter` — построение энтропии по измерениям временного джиттера (функции `rawFromJitter`, `seedFromJitter`).
- `http` — делает GET-запросы к URL'ам в `EntropySpec.HTTP`. Если тело ответа — 64 hex-символа, оно декодируется как байты; если это целое число — используется как int64; иначе хэшируется SHA256 и включается в микс. Для каждого URL также вычисляется per-HTTP seed, сохраняемый в `Provenance.PerHTTPSeeds`.
- `mix` — смешение нескольких источников (OS, jitter, HTTP). `deriveSeed` комбинирует несколько raw-кусочков энтропии и возвращает мастер-сид + набор per-HTTP seeds.

TRNG internals (из `trng.go` / `drbg.go`)
- TRNG — простой обёртка над `HMAC-DRBG` (HMAC-SHA256). Инициализация через `NewTRNGFromSeed(seed int64, per []int64)` или `NewTRNGFromTx(tx)`.
- HMAC-DRBG реализован в `drbg.go` и следует упрощённый SP800-90A-подобный рисунок: K/V инициируются, затем update(seedMaterial) и генерация через HMAC(K, V).

Псевдо-блокчейн и persist
- Транзакции и блоки хранятся в памяти (`txStore`, `chain`) и сериализуются в `store.json` (в текущий рабочий каталог). Вызов `saveStore()` пишет временный файл и переименовывает его в `store.json`.
- При старте `main()` пробует загрузить `store.json`; если файл отсутствует или повреждён, создаётся пустой `store.json`.

Подписи и хранение signing key
--------------------------------
Для обеспечения неизменности результатов и возможности доказать, что наборы чисел действительно были сгенерированы сервером, проект сохраняет HMAC-подписи для операций tier (лотерей/тиров).

Как это работает в коде:
- Для `/generate-tier` создаётся payload {seed, numbers, winners} и рассчитывается HMAC-SHA256 подпись по signing key.
- Подпись сохраняется в `Transaction.Signature` и также в поле `Transaction.Published` (т.е. попадает в блок и `Block.DataHash`).

Персистентность ключа:
- signing key хранится в `store.json` в поле `signing_key` как hex-строка по умолчанию.
- Для безопасности вы можете задать переменную окружения `SIGNING_KEY_PASSPHRASE`. В этом случае при сохранении ключ будет зашифрован AES-GCM (ключ для AES берётся из SHA256(passphrase)) и в `store.json` сохранится hex(nonce|ciphertext). При загрузке `loadStore()` будет пытаться расшифровать ключ, если `SIGNING_KEY_PASSPHRASE` задана.
- Если вы хотите, чтобы подписи были воспроизводимы между перезапусками, задайте постоянную фразу `SIGNING_KEY_PASSPHRASE` в окружении сервера или заранее положите hex-ключ в `store.json`.

Безопасность и рекомендации:
- Хранение ключа в `store.json` (особенно в незашифрованном виде) требует доверия к файловой системе — кто имеет доступ к файлу, может подделывать подписи. Рекомендуется использовать `SIGNING_KEY_PASSPHRASE` и хранить пассфразу в защищённом секретном хранилище.
- Для более строгих гарантий используйте HSM/KMS или подпись GPG вместо HMAC.

Новые endpoints (tier и подписи)
--------------------------------
- `GET /generate-tier?min=<min>&max=<max>&n=<n>&t=<t>&entropy=<mode>&seed=<seed>`
  - Генерирует уникальные n чисел в диапазоне [min..max] (без повторов) на основе TRNG.
  - Выбирает t уникальных победителей среди этих n с помощью TRNG.
  - Сохраняет транзакцию с `TierNumbers`, `TierWinners` и `Signature` и добавляет блок.
  - Возвращает JSON: `{tx_id, numbers, winners, signature}`.

- `GET /tx/{id}/tier`
  - Возвращает сохранённые `numbers`, `winners` и `signature` для транзакции.

- `GET /tx/{id}/verify-signature`
  - Пересчитывает payload {seed, numbers, winners} и проверяет HMAC-SHA256 подпись, используя signing key, восстановленный из `store.json` (и расшифрованный при наличии `SIGNING_KEY_PASSPHRASE`). Возвращает JSON с полем `signature_match` и ожидаемой/фактической подписью.

Пример (PowerShell)

```powershell
# rng-chaos — локальный детерминированный TRNG-сервер

Небольшой однопакетный Go-бинарник, реализующий детерминированный генератор случайных данных (TRNG) с возможностью смешивания нескольких источников энтропии и последующим "отбеливанием" (whitening). Проект удобен для локального аудита, воспроизведения прогонов и тестирования.

Ключевые идеи
- Источники энтропии: `os` (cryptographic RNG), `jitter` (тайминговый джиттер), `http` (входные URL), `mix` (комбинация) и `repro` (фиксированный seed для воспроизводимости).
- Детерминированный поток байт строится поверх HMAC-DRBG (HMAC-SHA256). Инициализация происходит из мастер-sid`а и опциональных per-HTTP seed'ов.
- Каждая генерация сохраняется как `Transaction` и индексируется в простом локальном псевдо-блокчейне (`Block`). Всё состояние сериализуется в `store.json`.

Требования
- Go 1.22 (указано в `go.mod`).

Сборка и запуск

От корня репозитория выполните (PowerShell):

```powershell
gofmt -w .
go build
```

Запуск сервера (из корня репозитория):

```powershell
go run .
```

Сервер по умолчанию слушает на :4040 (см. `main.go`). Таймауты в `main.go`:
- ReadTimeout / ReadHeaderTimeout: 5s
- WriteTimeout: 600s

HTTP API — кратко

- GET/POST /generate — основной endpoint для создания генерации. Параметры можно передать как query-параметры, так и JSON в теле (см. `GenerateParams` в `types.go`). Возвращает JSON с идентификатором транзакции.
- GET /tx/{id}/info — возвращает полную структуру `Transaction` (включая `Provenance.PerHTTPSeeds`).
- GET /tx/{id}/trng?n=<N>&format=hex|bytes — восстанавливает TRNG из `Transaction.Seed` и `Provenance` и отдаёт N байт в выбранном формате.
- GET /tx/{id}/stats — возвращает `SimulationData` (результаты симуляции), если она есть.
- GET /tx/{id}/verify — выполняет набор локальных проверок целостности и возвращает JSON с результатами.
- GET /txs — короткий список транзакций.
- GET /chain — возвращает псевдо-блокчейн.
- POST /stats/upload — вспомогательный endpoint для загрузки внешней статистики (используется в `tools/`).

Поля и форматы (основные структуры в `types.go`)
- GenerateParams — параметры генерации: `Count`, `CanvasW`, `CanvasH`, `Iterations`, `NumPoints`, `PixelWidth`, `Entropy` (см. EntropySpec), `Motion`, `Step`, `Whiten`.
- EntropySpec — `Mode` ("os"|"jitter"|"http"|"mix"|"repro"), `Seed64` (используется для `repro`), `HTTP` (список URL для режима `http`).
- Transaction — содержит `TxID`, `CreatedAt`, `Seed` (int64 мастер-seed), `Sim` (SimulationData), `DataHash`, `BitsHash`, `Published`, `Provenance` (GenerationProvenance) и, при необходимости, поля для "tier" (лотерей) и `Signature`.

Подробнее по энтропии и воспроизводимости

- repro: полностью детерминированный режим — `deriveSeed` вернёт `EntropySpec.Seed64` как мастер-seed. Используйте для тестов/vectors.
- os: чтение крипто-энтропии из системы (`crypto/rand`). Невоспроизводимо.
- jitter: собирает байты из таймингов и хеширует их (функции `rawFromJitter`, `seedFromJitter`).
- http: делает GET-запросы к каждому URL из `EntropySpec.HTTP` (3s таймаут). Поведение при чтении тела:
  - если тело — 64 hex-символа, оно декодируется как сырые байты и используется напрямую;
  - если тело — десятичное число, используется его little-endian int64 представление;
  - иначе тело хэшируется SHA256 и включается в микс;
  Для `mix` и `http` режимов код также формирует `PerHTTPSeeds` (int64) по каждому URL — они сохраняются в `Transaction.Provenance.PerHTTPSeeds` и нужны для воспроизводимости при отсутствии внешних URL.
- mix: комбинирует `rawFromOS`, `rawFromJitter` и результаты `rawFromHTTP` (если указаны URL) и получает мастер-seed через SHA256(sum||label).

TRNG (drbg)

- В проекте TRNG — тонкий слой над HMAC-DRBG (HMAC-SHA256). Инициализация делается через `NewTRNGFromSeed(seed int64, per []int64)` или `NewTRNGFromTx(tx *Transaction)` (см. `trng.go`).
- Для воспроизведения: сохраните `Seed` и `Provenance.PerHTTPSeeds` из `/tx/{id}/info`; затем `NewTRNGFromTx` даст тот же DRBG.

Псевдо-блокчейн и сохранение состояния

- Вся память приложения (транзакции и цепочка блоков) сериализуется в `store.json` в рабочем каталоге. При старте `main()` пытается загрузить `store.json` и при отсутствии создаёт пустой файл.
- `Block.Hash` считается как SHA256 строки `Index:Timestamp:TxID:DataHash:PrevHash` (см. `computeBlockHash`), и `validateChain()` проверяет целостность цепочки.

Tier / подписи

- В проекте есть поддержка простого "tier" (лотерейной) функционала: `/generate-tier` генерирует набор чисел и выбирает победителей с помощью TRNG. Результат подписывается HMAC-SHA256 signing key'ом и сохраняется в `Transaction.Signature`.
- signing key по-умолчанию хранится в `store.json` в виде hex; если требуется шифрование, задайте `SIGNING_KEY_PASSPHRASE` — тогда ключ будет сохранён в `store.json` как hex(nonce|ciphertext), где AES ключ получен из SHA256(passphrase).

Endpoint валидации `/tx/{id}/verify`

Возвращает JSON с флагами (пример):

```json
{
  "chain_valid": true,
  "tx_found": true,
  "data_hash_match": true,
  "bits_hash_match": true,
  "published_in_chain": true
}
```

Где:
- chain_valid — цепочка валидна по хешам;
- tx_found — транзакция найдена;
- data_hash_match — пересчитанный SHA256(JSON(SimulationData)) совпадает с сохранённым `DataHash`;
- bits_hash_match — пересчитанный SHA256(итоговых байт после whitening) совпадает с `BitsHash`;
- published_in_chain — значение `Transaction.Published` присутствует в соответствующем блоке цепочки.

Примеры (PowerShell)

Создать воспроизводимую генерацию и получить 64 байта в hex:

```powershell
$resp = Invoke-RestMethod -Uri 'http://localhost:4040/generate?entropy=repro&seed64=12345&whiten=aes' -Method Get
$txid = $resp.id
Invoke-RestMethod -Uri "http://localhost:4040/tx/$($txid)/trng?n=64&format=hex" -Method Get
Invoke-RestMethod -Uri "http://localhost:4040/tx/$($txid)/info" -Method Get | ConvertTo-Json -Depth 5
```

Пример curl + jq:

```bash
curl -s "http://localhost:4040/generate?entropy=repro&seed64=12345&whiten=aes" -o gen.json
jq . gen.json
id=$(jq -r .id gen.json)
curl "http://localhost:4040/tx/$id/trng?n=64&format=hex"
```

Инструменты

В `tools/` есть вспомогательные программы:
- `run_generate.go` — пример клиента, вызывающего `/generate` и затем `/tx/{id}/stats`.
- `run_generate_info.go` — пример, который показывает как получить `PerHTTPSeeds` и другие данные для воспроизводимости.

Рекомендации и ограничения

- Это локальная, нераспределённая структура — нет консенсуса и репликации. `store.json` следует резервировать и хранить в безопасном месте.
- Сохранение signing key в незашифрованном `store.json` небезопасно; используйте `SIGNING_KEY_PASSPHRASE` или внешние HSM/KMS для серьёзных случаев.
- Для воспроизводимости при режиме `http` сохраняйте `PerHTTPSeeds` или сохраняйте `store.json` сразу после генерации.

Следующие шаги (опционально)

- Добавить unit-тесты для `deriveSeed`, `NewTRNGFromTx` и endpoint'а `verify`.
- Экспортировать утилиты из `tools/` в отдельные бинарники для CI.

---

Файлы, которые стоит просмотреть в первую очередь:
- `main.go` — маршруты и конфигурация сервера.
- `entropy.go` — `deriveSeed`, `rawFromHTTP`, `rawFromJitter`.
- `trng.go`, `drbg.go` — HMAC-DRBG и инициализация TRNG.
- `types.go` — JSON структуры.

  - `run_generate.go` — пример запроса `/generate` и последующего извлечения статистики.
  - `run_generate_info.go` — пример получения `PerHTTPSeeds` для воспроизводимости.

Короткое описание архитектуры
- Поток: Источник энтропии -> Симуляция -> HMAC-DRBG (TRNG) -> Отбеливание (whitening) -> Транзакция и хранение.
- Детерминированность важна: режим `repro` и параметр `seed64` позволяют воссоздать конкретный прогон.
- Пер-HTTP seeds: при использовании HTTP-энтропии отдельные ответы приводят к детерминированным под-сидерам, которые сохраняются в транзакции для воспроизводимости.

Сборка и запуск

Требования
- Go 1.22 (указано в `go.mod`).

Сборка (в корне репозитория):

```powershell
gofmt -w .
go build
```

Запуск сервера:

```powershell
go run .
```

По умолчанию сервер слушает на `:4040` (см. `main.go`).

HTTP API

- `GET`/`POST /generate` — основной endpoint. Параметры (через query или JSON):
  # rng-chaos — Документация (подробная)

  Проект `rng-chaos` — однопакетный Go-бинарник, реализующий детерминированный TRNG-поток с возможностями воспроизводимости, смешения энтропии из разных источников и отбеливания (whitening) выходных данных.

  Содержимое репозитория (основное):
  - `main.go` — запуск HTTP-сервера, маршруты, загрузка/сохранение состояния.
  - `entropy.go` — источники энтропии и их комбинирование (`deriveSeed`, `rawFromHTTP`).
  - `trng.go`, `drbg.go` — HMAC-DRBG и обёртки для инициализации из seed/транзакции.
  - `types.go` — JSON-структуры: `SimulationData`, `GenerateParams`, `Transaction`, `GenerationProvenance`, `Block`.
  - `blockchain.go` / `store.json` — in-memory хранилище транзакций и сериализация на диск.
  - `stats.go`, `motion.go`, `render.go` — статистика, законы движения и рендер/симуляция.
  - `tools/` — утилиты для воспроизведения и отладки (например, `run_generate.go`, `run_generate_info.go`).

  Коротко об архитектуре
  - Поток: Источник энтропии -> Симуляция -> HMAC-DRBG (TRNG) -> Whitening -> Транзакция -> Сохранение в `store.json`.
  - Режим `repro` + `seed64` обеспечивает детерминированность (включая `PerHTTPSeeds` для HTTP-энтропии).

  Требования и запуск
  - Go 1.22 (см. `go.mod`).

  Сборка и запуск (в корне репозитория):

  ```powershell
  gofmt -w .
  go build
  go run .
  ```

  По умолчанию сервер слушает на `:4040` (см. `main.go`).

  HTTP API — кратко
  - `GET`/`POST /generate` — создать генерацию. Параметры (query или JSON): `entropy` (mode), `seed64` (для repro), `whiten` и параметры симуляции. Возвращает JSON с `id` транзакции.
  - `GET /tx/{id}/info` — полная информация о транзакции (включая `PerHTTPSeeds`).
  - `GET /tx/{id}/trng?n=<N>&format=hex|bytes` — прочитать N байт из TRNG для транзакции.
  - `GET /tx/{id}/stats` — статистика/результаты симуляции (`SimulationData`).

  Дальнейшие разделы описывают внутренности `/generate`, формат `store.json`, псевдо-блокчейн и что является цифровым отпечатком генерации (fingerprint).

  ## Внутреннее устройство `/generate` — пошагово

  Ниже — подробный поток событий при вызове `/generate`.

  1) Парсинг входа
    - Параметры десериализуются в `GenerateParams` (см. `types.go`): `Count`, `Entropy`, `Motion`, `Whiten`, и т.д.

  2) Сбор энтропии и derivation
    - В зависимости от `EntropySpec.Mode` (`os`, `jitter`, `http`, `mix`, `repro`) выбирается стратегия:
      - `os` — системный RNG (non-reproducible).
      - `jitter` — измерение таймингов/джиттера.
      - `http` — запросы к URL из `EntropySpec.HTTP`; ответы детерминируются в per-HTTP seed'ы.
      - `mix` — смешение нескольких источников.
      - `repro` — используется `EntropySpec.Seed64` как мастер-сид для полного воспроизведения.

    - `deriveSeed` (в `entropy.go`) агрегирует входные куски энтропии и возвращает финальный мастер-сид. При наличии HTTP-энтропии также вычисляются `PerHTTPSeeds` (массив int64) — они сохраняются в `GenerationProvenance.PerHTTPSeeds`.

  3) Инициализация TRNG (HMAC-DRBG)
    - Сгенерированный мастер-сид передаётся в DRBG (`NewTRNGFromSeed` / `NewTRNGFromTx`), который создаёт воспроизводимый поток байт.

  4) Симуляция
    - TRNG управляет параметрами симуляции; результат — `SimulationData` (пути точек, параметры канвы и т.д.).
    - JSON от `SimulationData` хэшируется SHA256 и сохраняется как `Transaction.DataHash`.

  5) Генерация окончательных байт и whitening
    - Генерируются запрошенные `Count` бит/байт из TRNG.
    - Если указан `Whiten` (`off`|`hmac`|`aes`|`hybrid`), к байтам применяется соответствующий отбеливающий алгоритм.
    - Итоговый байтовый массив хэшируется SHA256 и записывается в `Transaction.BitsHash`.

  6) Публикация — компактный отпечаток
    - Вычисляется `Published` как HKDF или аналог над `BitsHash` и `DataHash` (короткая сигнатура согласованности между симуляцией и битами).

  7) Сохранение транзакции и обновление цепочки
    - Формируется `Transaction` со всеми метаданными: `TxID`, `CreatedAt`, `Seed`, `Sim`, `DataHash`, `BitsHash`, `Published`, `Provenance`.
    - Создаётся новый `Block` для псевдо-блокчейна (см. `types.Block`), где `DataHash`/`Published` используется как полезная нагрузка.
    - Изменённое хранилище сериализуется в `store.json`.

  ## `/tx` endpoints и воспроизводимость

  - `GET /tx/{id}/info`
    - Возвращает полную структуру `Transaction`. Полезно для извлечения `Provenance.PerHTTPSeeds` и параметров симуляции.

  - `GET /tx/{id}/trng?n=<N>&format=hex|bytes`
    - Сервер восстанавливает TRNG из `Transaction.Seed` и `Provenance` и отдаёт N байт в указанном формате. Это гарантирует, что при том же `store.json` и неизменном коде вы получите тот же поток байт.

  - `GET /tx/{id}/stats`
    - Возвращает `SimulationData`/статистику для анализа и верификации.

  Пример воспроизведения (сохранённый в `store.json`):
  1) Вызов `/tx/{id}/info` — сохранить `Seed` и `PerHTTPSeeds`.
  2) В коде вызвать `NewTRNGFromTx(tx)` (см. `trng.go`) — это даст тот же DRBG, что и при генерации.
  3) Вызвать `/tx/{id}/trng?n=...` для получения потока без необходимости знать внутренние детали.

  ## Формат `store.json` и псевдо-блокчейн

  `store.json` содержит сериализованные структуры хранилища приложения, обычно включая:
  - массив `Transaction` (все транзакции/генерации);
  - массив `Block` — псевдо-блокчейн, где каждый `Block` ссылается на `PrevHash`.

  Структуры (см. `types.go`):
  - `Transaction` — основные поля: `TxID`, `CreatedAt`, `Count`, `Seed`, `Sim` (SimulationData), `DataHash`, `BitsHash`, `Published`, `Provenance`.
  - `Block` — `Index`, `Timestamp`, `TxID`, `DataHash` (published), `PrevHash`, `Hash`.

  Назначение ключевых полей:
  - `Seed` — мастер-сид (int64). Достаточен для восстановления DRBG в режиме `repro` (в сочетании с `PerHTTPSeeds`, если применимо).
  - `DataHash` — SHA256(JSON(SimulationData)). Подтверждает, что симуляция была выполнена однозначно.
  - `BitsHash` — SHA256(итоговых байт после whitening). Подтверждает целостность и неизменность итоговых данных.
  - `Published` — компактный отпечаток (HKDF/label) для быстрой проверки соответствия `DataHash` и `BitsHash`.
  - `Block.PrevHash` / `Block.Hash` — простая связка для проверки порядка и целостности цепочки.

  Почему "псевдо-блокчейн"?
  - Структура похожа на блокчейн (цепочка хэшей и транзакций), но без распределённого консенсуса и POW/Stake — это последовательная локальная история, удобная для аудита и верификации.

  ## Цифровой слепок (fingerprint) генерации

  Для однозначной идентификации и верификации результата используется комбинация полей:
  - `Seed` — мастер-сид;
  - `Provenance` — все параметры генерации (`Entropy`, `Motion`, `Iterations`, `NumPoints`, `PixelWidth`, `CanvasW`, `CanvasH`, `Step`, `Whiten`, `PerHTTPSeeds`);
  - `DataHash` — SHA256(JSON(SimulationData));
  - `BitsHash` — SHA256(итоговых байт после whitening);
  - `Published` — компактная HKDF-метка над `BitsHash` и `DataHash`.

  Если все перечисленные поля совпадают при повторном прогона/проверке — генерация считается воспроизведённой и валидной.

  ## Примеры запросов (PowerShell)

  Создать воспроизводимую генерацию и получить 64 байта в hex:

  ```powershell
  $resp = Invoke-RestMethod -Uri 'http://localhost:4040/generate?entropy=repro&seed64=12345&whiten=aes' -Method Get
  $txid = $resp.id
  Invoke-RestMethod -Uri "http://localhost:4040/tx/$($txid)/trng?n=64&format=hex" -Method Get
  Invoke-RestMethod -Uri "http://localhost:4040/tx/$($txid)/info" -Method Get | ConvertTo-Json -Depth 5
  ```

  Пример curl + jq:

  ```bash
  curl -s "http://localhost:4040/generate?entropy=repro&seed64=12345&whiten=aes" -o gen.json
  jq . gen.json
  id=$(jq -r .id gen.json)
  curl "http://localhost:4040/tx/$id/trng?n=64&format=hex"
  ```

  ## Контракт: вход/выход и ошибки

  - Вход: HTTP-параметры/JSON в соответствии с `GenerateParams`.
  - Успех: HTTP 200 + JSON с `id` транзакции; транзакция добавлена в `store.json`.
  - Ошибки:
    - 4xx — неверные параметры;
    - 5xx — внутренние ошибки (I/O при записи `store.json`, таймауты при `http`-энтропии и т.д.).

  ## Частые проблемы и рекомендации

  - Если используете `entropy=http`, сохраните `PerHTTPSeeds` и/или `store.json` — при недоступности внешних URL они понадобятся для воспроизведения.
  - При изменении алгоритма whitening старые `BitsHash` перестанут совпадать — храните `Provenance.Whiten` вместе с транзакцией.
  - Для тестов воспроизводимости используйте `repro` + фиксированный `seed64` и проверяйте `DataHash`/`BitsHash`.


## Описание JSON-ответа `/tx/{id}/verify`

Когда вы вызываете `GET /tx/{id}/verify`, сервер возвращает JSON с набором булевых флагов и дополнительной информацией для быстрой проверки целостности. Поля и их смысл:

- `chain_valid` (bool): результат проверки псевдо-блокчейна (`validateChain()`). `true` означает, что для каждого блока пересчитанный хэш совпадает с сохранённым и ссылки `PrevHash` корректны.
- `tx_found` (bool): `true`, если транзакция с указанным `tx_id` найдена в `txStore`.
- `data_hash_match` (bool): `true`, если пересчитанный `DataHash` совпадает с сохранённым в `Transaction.DataHash`. Важно: в текущей реализации `DataHash` вычисляется как `SHA256(pathDigest)` — то же самое используется при проверке, поэтому это поле индицирует, что симуляция воспроизводима и не была изменена.
- `bits_hash_match` (bool): `true`, если хэш итоговых бит (`BitsHash`) совпадает при пересчёте. `BitsHash` генерируется как SHA256 от битовой последовательности после применения режима `Whiten`.
- `published_in_chain` (bool): `true`, если значение `Transaction.Published` присутствует в поле `Block.DataHash` соответствующего блока цепочки (т.е. транзакция была «опубликована» в цепочку).

Пример ответа:

```json
{
  "chain_valid": true,
  "tx_found": true,
  "data_hash_match": true,
  "bits_hash_match": true,
  "published_in_chain": true
}
```

Если `data_hash_match` == `false`, это означает, что пересчитанный отпечаток симуляции не совпадает с сохранённым — возможные причины: несогласованность способа хеширования между генерацией и верификацией (устранено в коде), изменение кода `runSimulation` после генерации, или повреждение/изменение `store.json`.

