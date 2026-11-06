# TripGo Trip Service

Сервис поездок на Go и PostgreSQL. Во второй лабораторной код разделён на слои, добавлены тесты, структурные логи и OpenTelemetry. [Контракт API](contracts/openapi/trip-service.openapi.yaml).

## Запуск

Нужны Go 1.26+, Docker, make и tripgoctl. Для линтера - golangci-lint v2.

```sh
tripgoctl cluster start
tripgoctl environment start
tripgoctl connect
make migrate
make run
```

tripgoctl создаёт .env с адресами окружения. Настройки перечислены в [.env.example](.env.example), Makefile загружает их из .env. Адреса Jaeger и Grafana выводит `tripgoctl connect`.

Docker: `make docker-build`, затем `make docker-run`. Образ на distroless, около 42 МБ, запуск не от root. Для доступа к локальной БД используется `--network host` (Linux).

## Проверки

```sh
make test
make test-integration
make lint
```

make test запускает unit- и HTTP-тесты и выводит покрытие. make test-integration проверяет репозитории и транзакции на PostgreSQL. Все тесты идут с -race. Для интеграционных нужна DATABASE_URL или INTEGRATION_DATABASE_URL и права CREATE SCHEMA: тесты создают отдельные схемы и убирают их после проверки.

## Архитектура

HTTP вызывает usecase, usecase зависит от домена и интерфейсов репозиториев. Репозитории реализуют эти интерфейсы через PostgreSQL; домен не зависит от остальных слоёв. Интерфейсы лежат в contract.go у потребителя: он определяет, какие методы ему нужны. Обработчики и сценарии разбиты по действиям: create, get, finish. Зависимости собираются в container, подключение к БД и телеметрия лежат в infra.

Между слоями передаётся доменная Trip, поэтому отдельного model/ нет. Ответы и ошибки преобразуются в HTTP-слое. Сгенерированный код оставлен в api/, как в первой работе, чтобы не менять существующие импорты.

## Решения

- Read Committed: гонки создания закрывает частичный уникальный индекс по активному водителю, завершения - условие status = 'active' в UPDATE. Более строгая изоляция для этих операций не нужна.
- Менеджер транзакций передаёт транзакцию через context. При ошибке или панике делает rollback, при успехе - commit. Rollback ограничен DATABASE_QUERY_TIMEOUT и выполняется даже после отмены запроса.
- Idempotency-Key хранится 24 часа. Ключ, поездка и история создаются в одной транзакции; очистка протухших ключей запускается раз в час.
