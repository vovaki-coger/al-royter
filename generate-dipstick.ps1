#generate-dipstick.ps1
# Создаёт opencode.json со всеми моделями AL-Router для OpenCode / DeepSeek Harness
# Выполняется автоматически:
#   1. Загружает конфиг с https://al-royter.onrender.com/api/opencode-config
#   2. Записывает его в %USERPROFILE%\.config\opencode\opencode.json
#   3. Выводит инструкцию для DeepSeek Harness

# ────────────────────────────────────────────────────────────────────
# 1. Загружаем конфиг AL-Router
# ────────────────────────────────────────────────────────────────────
$apiUrl = "https://al-royter.onrender.com/api/opencode-config"
Write-Host "Получение конфигурации AL-Router с $apiUrl..." -ForegroundColor Yellow

$httpResponse = $null
$httpResponse = try {
    Invoke-RestMethod -Uri $apiUrl -Method Get -UseBasicParsing -TimeoutSec 60
} catch {
    Write-Error "Не удалось загрузить конфигурацию: $($_.Exception.Message)"
    return
}

$configJson = $httpResponse.json
if (-not $configJson) {
    Write-Error "Ответ API не содержит поля 'json'."
    return
}

# ────────────────────────────────────────────────────────────────────
# 2. Сохраняем в стандартный каталог OpenCode (DeepSeek Harness может
#    читать оттуда или ты добавишь вручную через UI)
# ────────────────────────────────────────────────────────────────────
$openCodeConfigDir = "$env:USERPROFILE\.config\opencode"
if (-not (Test-Path $openCodeConfigDir)) {
    New-Item -ItemType Directory -Path $openCodeConfigDir -Force | Out-Null
}
$openCodeConfigPath = "$openCodeConfigDir\opencode.json"
$configJson | Out-File -FilePath $openCodeConfigPath -Encoding UTF8 -Force
Write-Host "Сохранено: $openCodeConfigPath" -ForegroundColor Green

# ────────────────────────────────────────────────────────────────────
# 3. Выводим сводку и инструкцию для DeepSeek Harness
# ────────────────────────────────────────────────────────────────────
Write-Host "`n=== Список моделей AL-Router ===" -ForegroundColor Cyan
$cfg = $configJson | ConvertFrom-Json
$models = $cfg.providers['al-router'].models
Write-Host "Провайдер: $($cfg.providers['al-router'].name)"
Write-Host "Адрес: $($cfg.base_url)"
Write-Host "Модель по умолчанию: $($cfg.default_model)"
Write-Host "Всего моделей: $($models.Count)`n"

Write-Host "=== Как добавить AL-Router в DeepSeek Harness ===" -ForegroundColor Cyan
Write-Host "1. Запусти DeepSeek Harness."
Write-Host "2. Открой настройки моделей / провайдеров (Settings → Providers или аналогично)."
Write-Host "3. Добавь нового провайдера со следующими полями:"
Write-Host "   • Имя: AL-Router"
Write-Host "   • Base URL: $($cfg.base_url)"
Write-Host "   • API Key: твой Master Key с сайта al-royter.onrender.com"
Write-Host "     (Дашборд → '📋 Скопировать мастер-ключ')"
Write-Host "   • Модель: любая из списка выше, например `al-router/nvidia/nvidia/nemotron-3-ultra-550b-a55b`"
Write-Host ""
Write-Host "Если в интерфейсе DeepSeek Harness есть поле 'Provider ID' или 'Имя провайдера',"
Write-Host "укажи там `al-router` — тогда модели будут доступны как `al-router/название_модели`."
Write-Host ""
Write-Host "Готово! Теперь DeepSeek Harness ходит к твоим моделям через AL-Router." -ForegroundColor Green