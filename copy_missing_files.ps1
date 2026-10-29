# Скрипт для копирования недостающих файлов из \\NAS\LPub\Videos в M:\Videos

$sourcePath = "\\NAS\LPub\Videos"
$destPath = "M:\Videos"
$errorLog = "$env:TEMP\copy_errors.log"
$retryList = "$env:TEMP\retry_files.txt"

Write-Host "=== Скрипт копирования недостающих файлов ===" -ForegroundColor Cyan
Write-Host "Источник: $sourcePath" -ForegroundColor Yellow
Write-Host "Назначение: $destPath" -ForegroundColor Yellow
Write-Host ""

# Функция для проверки, существует ли файл или папка в целевом месте
function Test-FileOrDirectoryExists {
    param (
        [string]$path
    )

    # Проверяем полный путь
    if (Test-Path $path) {
        return $true
    }

    # Разбиваем путь на части и проверяем каждую часть
    $parts = $path -split '\\' | Where-Object { $_ -ne '' }
    $currentPath = ""

    foreach ($part in $parts) {
        $currentPath = Join-Path $currentPath $part
        if (Test-Path $currentPath) {
            continue
        }
        else {
            # Если часть не существует, возвращаем false и путь до неё
            return @{ exists = $false; path = $currentPath }
        }
    }

    return $false
}

# Функция для сравнения содержимого каталогов
function Get-MissingFiles {
    param (
        [string]$source,
        [string]$destination
    )

    $missingFiles = @()
    $missingDirs = @()

    # Получаем все файлы в исходной директории с полными путями
    $sourceFiles = Get-ChildItem -Path $source -File -Recurse -ErrorAction SilentlyContinue |
                   Select-Object FullName, @{Name="SourcePath";Expression={$_.FullName}},
                   @{Name="RelativePath";Expression={$_.FullName.Replace($source + "\", "")}},
                   Length, LastWriteTime

    # Получаем все файлы в целевой директории
    $destFiles = @{}
    if (Test-Path $destination) {
        Get-ChildItem -Path $destination -File -Recurse -ErrorAction SilentlyContinue |
        ForEach-Object {
            $relativePath = $_.FullName.Replace($destination + "\", "")
            $destFiles[$relativePath] = @{Path = $_.FullName; Length = $_.Length; LastWriteTime = $_.LastWriteTime}
        }
    }

    # Получаем все директории в исходной директории
    $sourceDirs = Get-ChildItem -Path $source -Directory -Recurse -ErrorAction SilentlyContinue |
                 Select-Object FullName, @{Name="RelativePath";Expression={$_.FullName.Replace($source + "\", "")}}

    # Получаем все директории в целевой директории
    $destDirs = @{}
    if (Test-Path $destination) {
        Get-ChildItem -Path $destination -Directory -Recurse -ErrorAction SilentlyContinue |
        ForEach-Object {
            $relativePath = $_.FullName.Replace($destination + "\", "")
            $destDirs[$relativePath] = $true
        }
    }

    # Ищем отсутствующие файлы
    foreach ($file in $sourceFiles) {
        $relativePath = $file.RelativePath
        if (-not $destFiles.ContainsKey($relativePath) -or
            $destFiles[$relativePath].Length -ne $file.Length -or
            $destFiles[$relativePath].LastWriteTime -ne $file.LastWriteTime) {
            $missingFiles += $file
        }
    }

    # Ищем отсутствующие директории
    foreach ($dir in $sourceDirs) {
        $relativePath = $dir.RelativePath
        if (-not $destDirs.ContainsKey($relativePath)) {
            $missingDirs += $dir
        }
    }

    return @{
        Files = $missingFiles
        Dirs = $missingDirs
    }
}

# Шаг 1: Получаем список недостающих файлов и папок
Write-Host "Шаг 1: Анализ недостающих файлов..." -ForegroundColor Green
$missing = Get-MissingFiles -source $sourcePath -destination $destPath

Write-Host "Найдено файлов для копирования: $($missing.Files.Count)" -ForegroundColor Cyan
Write-Host "Найдено папок для копирования: $($missing.Dirs.Count)" -ForegroundColor Cyan
Write-Host ""

# Шаг 2: Копируем недостающие файлы и папки
Write-Host "Шаг 2: Копирование недостающих файлов..." -ForegroundColor Green
$errorFiles = @()

# Копируем файлы
foreach ($file in $missing.Files) {
    # Создаем структуру каталогов в целевом месте
    $destDir = Join-Path $destPath $file.RelativePath
    $destDir = $destDir -replace '\\{2,}', '\'  # Убираем лишние обратные слеши
    $destDir = $destDir.TrimEnd('\')  # Убираем trailing slash

    # Создаем директорию, если нужно
    if (-not (Test-Path $destDir)) {
        try {
            New-Item -ItemType Directory -Path $destDir -Force | Out-Null
        }
        catch {
            Write-Host "  ERROR: Не удалось создать директорию $($destDir) - $($_.Exception.Message)" -ForegroundColor Red
            $errorFiles += @{
                Name = Split-Path $file.FullName -Leaf
                RelativePath = $file.RelativePath
                FullName = $file.FullName
                Type = "File"
            }
            continue
        }
    }

    try {
        # Используем полный путь к файлу как источник
        $sourceFile = $file.FullName
        $destFile = Join-Path $destDir (Split-Path $file.FullName -Leaf)

        # Экранируем специальные символы в пути назначения
        $destFileEscaped = $destFile -replace '\\', '\\'

        Copy-Item -LiteralPath $sourceFile -Destination $destFileEscaped -Force -ErrorAction Stop
        Write-Host "  OK: $($file.RelativePath)" -ForegroundColor Gray
    }
    catch {
        Write-Host "  ERROR: $($file.RelativePath) - $($_.Exception.Message)" -ForegroundColor Red
        $errorFiles += @{
            Name = Split-Path $file.FullName -Leaf
            RelativePath = $file.RelativePath
            FullName = $file.FullName
            Type = "File"
        }
    }
}

# Копируем директории
foreach ($dir in $missing.Dirs) {
    $destDir = Join-Path $destPath $dir.RelativePath
    $destDir = $destDir -replace '\\{2,}', '\'  # Убираем лишние обратные слеши
    $destDir = $destDir.TrimEnd('\')  # Убираем trailing slash

    try {
        # Экранируем специальные символы
        $destDirEscaped = $destDir -replace '\\', '\\'

        Copy-Item -LiteralPath $dir.FullName -Destination $destDirEscaped -Recurse -Force -ErrorAction Stop
        Write-Host "  OK: $($dir.RelativePath)" -ForegroundColor Gray
    }
    catch {
        Write-Host "  ERROR: $($dir.RelativePath) - $($_.Exception.Message)" -ForegroundColor Red
        $errorFiles += @{
            Name = Split-Path $dir.FullName -Leaf
            RelativePath = $dir.RelativePath
            FullName = $dir.FullName
            Type = "Directory"
        }
    }
}

Write-Host ""
Write-Host "=== Результат первого прохода ===" -ForegroundColor Cyan
Write-Host "Успешно скопировано: $($missing.Files.Count + $missing.Dirs.Count - $errorFiles.Count)" -ForegroundColor Green
Write-Host "Ошибок при копировании: $($errorFiles.Count)" -ForegroundColor Red
Write-Host ""

# Если есть ошибки, сохраняем список для повторной попытки
if ($errorFiles.Count -gt 0) {
    $retryList | Out-File -FilePath $retryList -Encoding UTF8

    Write-Host "Ошибки сохранены в: $retryList" -ForegroundColor Yellow

    # Шаг 3: Повторная попытка копирования только ошибочных файлов
    Write-Host ""
    Write-Host "Шаг 3: Повторная попытка копирования ошибочных файлов..." -ForegroundColor Green
    $retryErrors = @()

    foreach ($error in $errorFiles) {
        try {
            if ($error.Type -eq "Directory") {
                $destDir = Join-Path $destPath $error.RelativePath
                $destDir = $destDir -replace '\\{2,}', '\'  # Убираем лишние обратные слеши
                $destDir = $destDir.TrimEnd('\')  # Убираем trailing slash

                # Экранируем специальные символы
                $destDirEscaped = $destDir -replace '\\', '\\'

                Copy-Item -LiteralPath $error.FullName -Destination $destDirEscaped -Recurse -Force -ErrorAction Stop
                Write-Host "  OK: $($error.RelativePath)" -ForegroundColor Green
            }
            else {
                # Создаем структуру каталогов
                $destDir = Join-Path $destPath $error.RelativePath
                $destDir = $destDir -replace '\\{2,}', '\'  # Убираем лишние обратные слеши
                $destDir = $destDir.TrimEnd('\')  # Убираем trailing slash

                if (-not (Test-Path $destDir)) {
                    New-Item -ItemType Directory -Path $destDir -Force | Out-Null
                }

                $destFile = Join-Path $destDir (Split-Path $error.FullName -Leaf)
                $destFileEscaped = $destFile -replace '\\', '\\'

                Copy-Item -LiteralPath $error.FullName -Destination $destFileEscaped -Force -ErrorAction Stop
                Write-Host "  OK: $($error.RelativePath)" -ForegroundColor Green
            }
        }
        catch {
            Write-Host "  ERROR: $($error.RelativePath) - $($_.Exception.Message)" -ForegroundColor Red
            $retryErrors += $error
        }
    }

    Write-Host ""
    Write-Host "=== Результат повторной попытки ===" -ForegroundColor Cyan
    Write-Host "Успешно скопировано после повторной попытки: $($errorFiles.Count - $retryErrors.Count)" -ForegroundColor Green
    Write-Host "Ошибок после повторной попытки: $($retryErrors.Count)" -ForegroundColor Red

    if ($retryErrors.Count -gt 0) {
        Write-Host ""
        Write-Host "=== Файлы, которые не удалось скопировать ===" -ForegroundColor Red
        foreach ($err in $retryErrors) {
            Write-Host "  - $($err.RelativePath)" -ForegroundColor Yellow
        }

        # Создаем подробный отчет
        $reportPath = Join-Path $env:TEMP "copy_error_report_$(Get-Date -Format 'yyyyMMdd_HHmmss').txt"
        "=== Файлы, которые не удалось скопировать ===" | Out-File -FilePath $reportPath -Encoding UTF8
        "" | Out-File -FilePath $reportPath -Append -Encoding UTF8
        foreach ($err in $retryErrors) {
            "Относительный путь: $($err.RelativePath)" | Out-File -FilePath $reportPath -Append -Encoding UTF8
            "Полный путь: $($err.FullName)" | Out-File -FilePath $reportPath -Append -Encoding UTF8
            "" | Out-File -FilePath $reportPath -Append -Encoding UTF8
        }

        Write-Host "Подробный отчет сохранен в: $reportPath" -ForegroundColor Yellow
    }
}
else {
    Write-Host "Все файлы успешно скопированы!" -ForegroundColor Green
}

Write-Host ""
Write-Host "=== Скрипт завершен ===" -ForegroundColor Cyan
Write-Host "Нажмите любую клавишу для выхода..." -ForegroundColor Gray
$null = $Host.UI.RawUI.ReadKey("NoEcho,IncludeKeyDown")
