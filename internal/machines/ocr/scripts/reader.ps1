# reader.ps1: reads words and their boxes from an image with the WinRT
# OCR engine (Windows' own). Argument 1: the PNG path. Output is the
# shared TSV every OCR engine prints, one word per line:
#
#   text`tx`ty`tw`th
#
# Boxes are pixels, top-left origin, matching the frame.
param([string]$Path)

$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Runtime.WindowsRuntime

$null = [Windows.Storage.StorageFile, Windows.Storage, ContentType = WindowsRuntime]
$null = [Windows.Media.Ocr.OcrEngine, Windows.Media.Ocr, ContentType = WindowsRuntime]
$null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics.Imaging, ContentType = WindowsRuntime]

$asTaskGeneric = ([System.WindowsRuntimeSystemExtensions].GetMethods() | `
  Where-Object { $_.Name -eq "AsTask" -and $_.GetParameters().Count -eq 1 -and `
  $_.GetParameters()[0].ParameterType.Name -eq "IAsyncOperation`1" })[0]

function Await($WinRtTask, $ResultType) {
  $netTask = $asTaskGeneric.MakeGenericMethod($ResultType).Invoke($null, @($WinRtTask))
  $netTask.Wait()
  return $netTask.Result
}

try {
  $file = [Windows.Storage.StorageFile]::GetFileFromPathAsync($Path)
  $file = Await ($file) ([Windows.Storage.StorageFile])
  $stream = Await ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) `
    ([Windows.Storage.Streams.IRandomAccessStream])
  $decoder = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) `
    ([Windows.Graphics.Imaging.BitmapDecoder])
  $bmp = Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
} catch {
  Write-Output ("note`tcannot load image: " + $_.Exception.Message)
  exit 0
}

$engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages()
if ($null -eq $engine) {
  Write-Output "note`tno OCR language pack for the user's languages"
  exit 0
}
$result = $engine.RecognizeAsync($bmp).GetAwaiter().GetResult()
$scaleW = $bmp.PixelWidth / $bmp.PixelWidth  # boxes below are pixels already
foreach ($line in $result.Lines) {
  foreach ($w in $line.Words) {
    $r = $w.BoundingRect
    $text = ($w.Text -replace "`t", " ")
    Write-Output ("{0}`t{1}`t{2}`t{3}`t{4}" -f $text, [int][Math]::Floor($r.X), `
      [int][Math]::Floor($r.Y), [int][Math]::Floor($r.Width), [int][Math]::Floor($r.Height))
  }
}
