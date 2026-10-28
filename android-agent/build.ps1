$ErrorActionPreference = 'Stop'

$sdkDir = "C:\Users\ElvisSouza\AppData\Local\Android\Sdk"
$buildTools = "$sdkDir\build-tools\36.0.0"
$platformJar = "$sdkDir\platforms\android-37.0\android.jar"
$jdkBin = "C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot\bin"
$debugKs = "C:\Users\ElvisSouza\.android\debug.keystore"

$agentDir = "W:\ReposHomeLab\fastexec\android-agent"
Set-Location $agentDir

$buildDir = "$agentDir\build"
if (Test-Path $buildDir) { Remove-Item -Recurse -Force $buildDir }
New-Item -ItemType Directory -Path "$buildDir\res", "$buildDir\gen", "$buildDir\obj", "$buildDir\dex" | Out-Null

Write-Host "1. Compiling resources with aapt2..."
& "$buildTools\aapt2.exe" compile "$agentDir\res\values\strings.xml" -o "$buildDir\res\"
& "$buildTools\aapt2.exe" compile "$agentDir\res\xml\accessibility_service_config.xml" -o "$buildDir\res\"
if ($LASTEXITCODE -ne 0) { throw "aapt2 compile failed" }

Write-Host "2. Linking resources..."
$flatFiles = Get-ChildItem "$buildDir\res" -Filter "*.flat" | Select-Object -ExpandProperty FullName
& "$buildTools\aapt2.exe" link -o "$buildDir\unaligned.apk" `
    -I $platformJar `
    --manifest "$agentDir\AndroidManifest.xml" `
    $flatFiles `
    -A "$agentDir\assets" `
    --java "$buildDir\gen" `
    --min-sdk-version 21 `
    --target-sdk-version 34
if ($LASTEXITCODE -ne 0) { throw "aapt2 link failed" }

Write-Host "3. Compiling Java sources..."
$javaFiles = Get-ChildItem -Path "$agentDir\src", "$buildDir\gen" -Filter "*.java" -Recurse | Select-Object -ExpandProperty FullName
& "$jdkBin\javac.exe" -d "$buildDir\obj" -cp $platformJar --release 8 $javaFiles
if ($LASTEXITCODE -ne 0) { throw "javac failed" }

Write-Host "4. Converting bytecode to DEX with d8..."
$classFiles = Get-ChildItem -Path "$buildDir\obj" -Filter "*.class" -Recurse | Select-Object -ExpandProperty FullName
& "$buildTools\d8.bat" --output "$buildDir\dex" --min-api 21 $classFiles
if ($LASTEXITCODE -ne 0) { throw "d8 failed" }

Write-Host "5. Adding classes.dex and native lib to APK..."
& "$jdkBin\jar.exe" uf "$buildDir\unaligned.apk" -C "$buildDir\dex" classes.dex

$libDir = "$buildDir\lib\armeabi-v7a"
New-Item -ItemType Directory -Path $libDir -Force | Out-Null
Copy-Item "$agentDir\assets\server-exec" "$libDir\libserver-exec.so"
& "$jdkBin\jar.exe" uf "$buildDir\unaligned.apk" -C "$buildDir" lib/armeabi-v7a/libserver-exec.so
if ($LASTEXITCODE -ne 0) { throw "jar add dex failed" }

Write-Host "6. Aligning APK with zipalign..."
& "$buildTools\zipalign.exe" -p -f 4 "$buildDir\unaligned.apk" "$buildDir\fastexec-agent.apk"
if ($LASTEXITCODE -ne 0) { throw "zipalign failed" }

Write-Host "7. Signing APK with apksigner..."
& "$buildTools\apksigner.bat" sign --ks $debugKs --ks-pass pass:android --key-pass pass:android "$buildDir\fastexec-agent.apk"
if ($LASTEXITCODE -ne 0) { throw "apksigner failed" }

Write-Host "SUCCESS! APK built at: $buildDir\fastexec-agent.apk"
Get-Item "$buildDir\fastexec-agent.apk" | Select-Object Name, Length, LastWriteTime
