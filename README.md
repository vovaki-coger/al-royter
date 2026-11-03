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
| `GIN_MODE` | Режим Gin | `release` |

## Лицензия

MIT