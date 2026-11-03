# AL-Router — Unified AI Gateway

Единый шлюз для доступа к моделям NVIDIA Build, OpenCode, DeepSeek, Hardness и другим провайдерам через **один мастер-ключ**. Полная интеграция с OpenRouter.

## Возможности

- 🔑 **Один мастер-ключ** — доступ ко всем провайдерам
- 🤖 **Авто-детекция моделей** — подхватывает модели от провайдеров при вводе ключа
- 🔀 **OpenRouter Ready** — готовая конфигурация для подключения к OpenRouter
- 🌐 **Веб-интерфейс** — управление ключами, моделями, синхронизация
- 🐳 **Docker + Render** — деплой за минуты
- 🔒 **Безопасность** — JWT токены, bcrypt, изолированные ключи

## Быстрый старт

### Локально (Docker Compose)

```bash
git clone https://github.com/vovaki-coger/al-royter.git
cd al-royter
docker compose up -d
# Откройте http://localhost:8080
```

### На Render.com

1. Fork этот репозиторий
2. В Render: **New → Web Service** → подключите репо
3. Build Command: `docker build -t al-royter .`
4. Start Command: `./al-royter`
5. Добавьте Environment Variables:
   - `JWT_SECRET` — случайная строка (минимум 32 символа)
   - `PORT` — `8080` (ставится автоматически)
6. Deploy!

## Архитектура

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│   Client    │────▶│  AL-Router   │────▶│  Providers      │
│ (OpenRouter,│     │  (Go/Gin)    │     │  • NVIDIA Build │
│  OpenCode,  │     │              │     │  • OpenCode     │
│  Custom)    │     │  • Auth      │     │  • DeepSeek     │
└─────────────┘     │  • Router    │     │  • Hardness     │
                    │  • Model Reg │     │  • Custom       │
                    └──────────────┘     └─────────────────┘
```

## API Endpoints

| Метод | Путь | Описание |
|-------|------|----------|
| GET | `/v1/health` | Проверка здоровья |
| GET | `/v1/models` | Список всех моделей (OpenAI формат) |
| POST | `/v1/chat/completions` | Чат (требует мастер-ключ) |
| POST | `/v1/chat/completions/stream` | Стриминг чат |
| POST | `/auth/register` | Регистрация |
| POST | `/auth/login` | Вход |
| GET | `/api/me` | Профиль пользователя |
| GET | `/api/providers` | Список провайдеров |
| POST | `/api/providers/:name/key` | Сохранить ключ провайдера |
| DELETE | `/api/providers/:name/key` | Удалить ключ |
| POST | `/api/nvidia-key` | Сохранить NVIDIA ключ |
| GET | `/api/openrouter-config` | Конфиг для OpenRouter |
| GET | `/api/models` | Модели пользователя |
| POST | `/api/sync` | Принудительная синхронизация |

## Формат модели в запросах

```
POST /v1/chat/completions
Authorization: Bearer YOUR_MASTER_KEY
{
  "model": "nvidia/nemotron-3-ultra",
  "messages": [{"role": "user", "content": "Hello!"}],
  "stream": true
}
```

Формат: `provider/model-id` (например: `nvidia/nemotron-3-ultra`, `deepseek/deepseek-chat`, `opencode/gpt-4`)

## База данных

На Render диск временный — без внешней БД аккаунты и ключи стираются при каждом деплое.

### Neon (бесплатно навсегда, рекомендуем)

1. [neon.tech](https://neon.tech) → регистрация → **Create project**
2. Скопируй **Connection string** (выглядит как `postgresql://user:pass@ep-xxx.neon.tech/neondb?sslmode=require`)
3. Render → Environment → добавь `DATABASE_URL` = эта строка
4. Redeploy

Можно вставить и **Neon API key** (`napi_...`) — сервер сам найдёт первый проект и получит строку подключения через Neon API.

Схема создастся сама при старте. Если `DATABASE_URL` не задан — используется локальный SQLite (только для разработки).

### Альтернативы
- **Supabase** — free Postgres 500 МБ
- **Render PostgreSQL** — только платно (free-план истекает через 90 дней)

## Вход через Google

Кнопка «Продолжить с Google» на экране входа. Чтобы включить:

1. [Google Cloud Console](https://console.cloud.google.com/apis/credentials) → **Create Credentials → OAuth client ID → Web application**
2. **Authorized redirect URI**: `https://ВАШ_ДОМЕН/auth/google/callback`
   (для локалки: `http://localhost:8080/auth/google/callback`)
3. В Render добавьте переменные:
   - `GOOGLE_CLIENT_ID`
   - `GOOGLE_CLIENT_SECRET`
   - `GOOGLE_REDIRECT_URL` (необязательно, если домен стандартный)
4. Redeploy. Кнопка станет активной.

Аккаунт создаётся автоматически при первом входе. Если email уже есть — произойдёт вход в существующий аккаунт.

## Подключение к OpenRouter

1. Зарегистрируйтесь в AL-Router → получите **Master Key**
2. Откройте [OpenRouter Settings → Keys](https://openrouter.ai/settings/keys)
3. Нажмите **Add Provider** / **Create API Key**
4. Заполните:
   - **Name**: `AL-Router`
   - **Base URL**: `https://YOUR_DOMAIN.onrender.com/v1`
   - **API Key**: `ваш_мастер_ключ`
   - **Models**: `auto`
5. Готово! Все модели доступны в OpenRouter.

## Добавление своих провайдеров

Отредактируйте `config.yaml`:

```yaml
providers:
  - name: "my-llm"
    api_base: "https://api.my-llm.com/v1"
    models_endpoint: "/models"
    auth_type: "bearer"  # или "api_key"
```

Пересоберите и задеплойте.

## Структура проекта

```
al-royter/
├── cmd/server/main.go       # Точка входа
├── internal/
│   ├── auth/                # JWT, bcrypt, middleware
│   ├── config/              # Загрузка config.yaml
│   ├── models/              # Реестр моделей, синхронизация
│   ├── providers/           # HTTP клиенты для провайдеров
│   ├── router/              # Gin роуты, handlers
│   └── storage/             # SQLite: пользователи, ключи, модели
├── web/
│   ├── static/              # CSS, JS
│   └── templates/           # HTML (Go templates)
├── config.yaml              # Конфигурация
├── Dockerfile               # Multi-stage build
├── docker-compose.yml       # Локальная разработка
└── render.yaml              # Render.com Blueprint
```

## Переменные окружения

| Переменная | Описание | По умолчанию |
|------------|----------|--------------|
| `PORT` | Порт сервера | `8080` |
| `HOST` | Хост | `0.0.0.0` |
| `JWT_SECRET` | Секрет для JWT | **обязательно** |
| `DATABASE_URL` | Postgres DSN (Neon/Supabase) | нет (тогда SQLite) |
| `GOOGLE_CLIENT_ID` | Google OAuth Client ID | нет |
| `GOOGLE_CLIENT_SECRET` | Google OAuth Secret | нет |
| `GOOGLE_REDIRECT_URL` | Redirect URI (по умолчанию авто) | нет |
| `ALLOW_REGISTRATION` | Разрешить регистрацию по email | `true` |
| `GIN_MODE` | Режим Gin | `release` |

## Безопасность и правовые ограничения

Что уже сделано в коде:

- ограничение частоты запросов: отдельно по IP для входа/регистрации, отдельно по Master Key для `/v1`;
- лимит размера тела запроса (8 МБ на API, 64 КБ на авторизацию);
- security-заголовки (`nosniff`, `X-Frame-Options: DENY`, HSTS, Referrer-Policy);
- таймауты HTTP-сервера и автоочистка истёкших сессий;
- ключи провайдеров никогда не пишутся в логи;
- страницы `/terms` и `/privacy`.

Что кодом не решается — это твоя зона ответственности:

- **Условия провайдеров.** Бесплатные ключи NVIDIA Build выдаются для личного использования. Публичный шлюз, через который чужие люди ходят твоим ключом, — основание для блокировки ключа, а не «фича».
- **Перепродажа доступа.** Если берёшь деньги за доступ к чужим моделям без договора с провайдером — это уже не техническая, а юридическая проблема.
- **Чужие данные.** Открытая регистрация означает, что ты хранишь email и API-ключи посторонних людей. Для домашнего проекта это допустимо, для публичного нужен нормальный оператор данных.
- **Ключи в открытом виде.** API-ключи провайдеров лежат в базе как есть. При утечке БД утекут и они.

Практичный минимум: держать `ALLOW_REGISTRATION=false`, ходить только своим ключом и не публиковать ссылку в открытых каталогах.

## Лицензия

MIT