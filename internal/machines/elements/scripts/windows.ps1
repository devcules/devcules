# walker.ps1: the focused window's UI Automation tree as a flat TSV
# (the same shape as the other walkers). Argument 1: max elements.
# Runs under Windows PowerShell (the .NET UIAutomation types are not
# in pwsh7 by default). Prints a note when the desktop is unreachable
# (lock screen, UAC prompt) instead of failing.
param([int]$Max = 400)

$ErrorActionPreference = "Stop"
try {
  Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes
} catch {
  Write-Output "note`tUIAutomation types unavailable: $($_.Exception.Message)"
  exit 0
}

function Clean([string]$s) {
  $t = ($s -replace "`t", " " -replace "`r|`n", " ").Trim()
  if ($t.Length -gt 120) { $t = $t.Substring(0, 120) }
  return $t
}

$RoleNames = @{
  "Button" = "button"; "CheckBox" = "checkbox"; "RadioButton" = "radio";
  "Edit" = "field"; "Document" = "text"; "Text" = "text"; "Hyperlink" = "link";
  "Image" = "image"; "MenuItem" = "menuitem"; "Menu" = "menu"; "MenuBar" = "menu";
  "DataItem" = "row"; "ListItem" = "row"; "TreeItem" = "row"; "TabItem" = "tab";
  "Window" = "window"; "Slider" = "slider"; "ToolBar" = "toolbar"; "List" = "list";
  "Table" = "list"; "Tree" = "list"; "Pane" = ""; "Group" = ""; "Custom" = "";
  "TitleBar" = ""; "ScrollBar" = ""; "StatusBar" = ""
}
$WantEmpty = @("button", "field", "checkbox", "radio", "image", "tab", "slider")

try {
  $root = [System.Windows.Automation.AutomationElement]::FocusedElement
} catch {
  Write-Output "note`tno focused element (locked screen or secure desktop?)"
  exit 0
}
if ($null -eq $root) {
  Write-Output "note`tno focused element"
  exit 0
}

# Walk from the focused element's top-level window so the tree is one
# app's, like the other walkers.
try {
  $walker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
  $w = $root
  for ($i = 0; $i -lt 20; $i++) {
    $p = $walker.GetParent($w)
    if ($null -eq $p) { break }
    $w = $p
  }
  $root = $w
} catch { }

$count = 0
$truncated = $false
$sb = New-Object System.Text.StringBuilder

function Walk($el, [int]$depth) {
  if ($script:count -ge $Max -or $depth -gt 30) { $script:truncated = $true; return }
  try {
    $role = $RoleNames[$el.Current.ControlType.ProgrammaticName -replace "^ControlType\.", ""]
    if ($role) {
      $r = $el.Current.BoundingRectangle
      $onscreen = ($r.Width -ge 2 -and $r.Height -ge 2 -and $r.X + $r.Width -gt 0 -and $r.Y + $r.Height -gt 0)
      $title = Clean $el.Current.Name
      $value = ""
      if ($role -eq "password") { $title = "" } else {
        try {
          $vp = $el.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern) -as [System.Windows.Automation.ValuePattern]
          if ($vp) { $value = Clean $vp.Current.Value }
        } catch { }
      }
      if ($onscreen -and (($title -or $value) -or $WantEmpty -contains $role)) {
        $enabled = 1; $focused = 0
        try { if (-not $el.Current.IsEnabled) { $enabled = 0 } } catch { }
        try { if ($el.Current.HasKeyboardFocus) { $focused = 1 } } catch { }
        [void]$sb.AppendLine(("el`t{0}`t{1}`t{2}`t{3}`t{4}`t{5}`t{6}`t{7}`t{8}" -f `
          $role, [int]$r.X, [int]$r.Y, [int]$r.Width, [int]$r.Height, $enabled, $focused, $title, $value))
        $script:count++
      }
    }
    $kids = $el.FindAll([System.Windows.Automation.TreeScope]::Children, [System.Windows.Automation.Condition]::TrueCondition)
    foreach ($k in $kids) { Walk $k ($depth + 1) }
  } catch { }
}

try { Write-Output ("front`t" + (Clean $root.Current.Name)) } catch { }
Walk $root 0
if ($truncated -and $script:count -ge $Max) {
  Write-Output ("note`ttruncated at " + $Max + " elements")
}
Write-Output $sb.ToString().TrimEnd("`r", "`n")
