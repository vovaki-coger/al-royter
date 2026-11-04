#generate-opencode.ps1
# Сгенерируй opencode.json со всеми моделями AL-Router для OpenCode
# Выполняется автоматически:
#   1. Загружает конфиг с https://al-royter.onrender.com/api/opencode-config
#   2. Записывает его в %USERPROFILE%\.config\opencode\opencode.json
#   3. Выводит список моделей и инструкцию для OpenCode

# ---------------------------------------------------------------
# 1. Загружаем конфиг с хоста AL-Router (публично доступен)
$apiUrl = "https://al-royter.onrender.com/api/opencode-config"
Write-Host "Получение конфигурации OpenCode с $apiUrl..." -ForegroundColor Yellow

$httpResponse = $null
$httpResponse = try {
    Invoke-RestMethod -Uri $apiUrl -Method Get -UseBasicParsing -TimeoutSec 60
} catch {
    Write-Error "Не удалось загрузить конфигурацию: $($_.Exception.Message)"
    return
}

# API возвращает объект с полем "json" (JSON-строка конфига)
$configJson = $httpResponse.json
if (-not $configJson) {
    Write-Error "Ответ API не содержит поля 'json'."
    return
}

# ---------------------------------------------------------------
# 2. Сохраняем файл конфигурации в стандартном каталоге OpenCode
$openCodeConfigDir = "$env:USERPROFILE\.config\opencode"
if (-not (Test-Path $openCodeConfigDir)) {
    New-Item -ItemType Directory -Path $openCodeConfigDir -Force | Out-Null
}
$openCodeConfigPath = "$openCodeConfigDir\opencode.json"
$configJson | Out-File -FilePath $openCodeConfigPath -Encoding UTF8 -Force
Write-Host "Сохранено: $openCodeConfigPath" -ForegroundColor Green

# ---------------------------------------------------------------
# 3. Выводим удобную сводку
Write-Host "`n=== Список моделей AL-Router в OpenCode ===" -ForegroundColor Cyan
$cfg = $configJson | ConvertFrom-Json
$models = $cfg.providers['al-router'].models
Write-Host "Провайдер: $($cfg.providers['al-router'].name)"
Write-Host "Адрес: $($cfg.base_url)"
Write-Host "Выбрана модель по умолчанию: $($cfg.default_model)"
Write-Host "Всего моделей: $($models.Count)`n"

Write-Host "Вы можете:
   1. Запустить OpenCode (например, кликнуть по OpenCode.exe)
   2. Перейти к файлу в проводнике → .config\opencode\opencode.json
   3. Выбрать файл → "/models" → провайдер AL-Router и модель для этого сеанса
`nПоскольку /api/opencode-config защищён мастер-ключом, убедитесь, что у вас есть активная сессия на сайте al-royter.onrender.com (или используйте мастер-ключ напрямую)." -ForegroundColor White

# ---------------------------------------------------------------
# 4. Дополнительно: запускаем OpenCode (если найдено)
$openCodeExe = Get-ChildItem "$env:LOCALAPPDATA\Programs\ai.opencode.desktop\OpenCode.exe" -ErrorAction SilentlyContinue
if ($openCodeExe) {
    Write-Host "Запуск OpenCode..." -ForegroundColor Yellow
    Start-Process $openCodeExe.FullName -Wait:$false
} else {
    Write-Warning "OpenCode.exe не найден; установите OpenCode вручную, затем загрузите этот файл конфигурации."
}