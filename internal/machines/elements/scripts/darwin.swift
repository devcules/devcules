// walker.swift: the frontmost application's accessibility tree as a
// flat TSV, run by the Devcules helper (which holds the Accessibility
// permission). Argument 1: max elements. Lines:
//
//   size\tW\tH      native screen pixels (points x backing scale)
//   front\tapp      the frontmost application
//   window\ttitle   its focused window
//   el\trole\tx\ty\tw\th\tenabled\tfocused\ttitle\tvalue
//   note\ttext      why the tree is empty (permission, truncation, ...)
//
// Coordinates are native pixels, top-left origin, matching screen
// captures. Values of password fields never leave this script.
import AppKit
import ApplicationServices

let maxEls = CommandLine.arguments.count > 1 ? (Int(CommandLine.arguments[1]) ?? 400) : 400

guard AXIsProcessTrusted() else {
  print("note\tno accessibility permission: grant it to the Devcules helper in System Settings, Privacy & Security, Accessibility")
  exit(0)
}

let screen = NSScreen.main ?? NSScreen.screens[0]
let scale = screen.backingScaleFactor
print("size\t\(Int(screen.frame.width * scale))\t\(Int(screen.frame.height * scale))")

func attr(_ el: AXUIElement, _ name: String) -> AnyObject? {
  var v: AnyObject?
  return AXUIElementCopyAttributeValue(el, name as CFString, &v) == .success ? v : nil
}
func str(_ v: AnyObject?) -> String {
  guard let v = v else { return "" }
  if let s = v as? String { return s }
  if let n = v as? NSNumber { return n.stringValue }
  if let a = v as? NSAttributedString { return a.string }
  return ""
}
func clean(_ s: String, _ n: Int = 120) -> String {
  let t = s.replacingOccurrences(of: "\t", with: " ").replacingOccurrences(of: "\n", with: " ").trimmingCharacters(in: .whitespaces)
  return t.count > n ? String(t.prefix(n)) : t
}
func roleName(_ r: String) -> String {
  switch r {
  case "AXButton", "AXPopUpButton", "AXMenuButton": return "button"
  case "AXTextField", "AXTextArea", "AXComboBox", "AXSearchField": return "field"
  case "AXSecureTextField": return "password"
  case "AXCheckBox": return "checkbox"
  case "AXRadioButton": return "radio"
  case "AXStaticText": return "text"
  case "AXLink": return "link"
  case "AXImage": return "image"
  case "AXMenuItem", "AXMenuBarItem": return "menuitem"
  case "AXMenu", "AXMenuBar": return "menu"
  case "AXRow", "AXCell", "AXOutlineRow": return "row"
  case "AXTab", "AXTabGroup": return "tab"
  case "AXWindow", "AXSheet", "AXDrawer": return "window"
  case "AXSlider", "AXValueIndicator": return "slider"
  case "AXToolbar": return "toolbar"
  case "AXList", "AXTable", "AXOutline", "AXBrowser": return "list"
  case "AXHeading": return "heading"
  case "AXWebArea": return "web"
  default: return ""
  }
}

var out: [String] = []
var count = 0
var truncated = false

func walk(_ el: AXUIElement, _ depth: Int) {
  if count >= maxEls || depth > 30 {
    truncated = true
    return
  }
  let role = str(attr(el, kAXRoleAttribute))
  let name = roleName(role)
  if !name.isEmpty {
    var pos = CGPoint.zero, size = CGSize.zero
    if let p = attr(el, kAXPositionAttribute) { AXValueGetValue(p as! AXValue, .cgPoint, &pos) }
    if let s = attr(el, kAXSizeAttribute) { AXValueGetValue(s as! AXValue, .cgSize, &size) }
    let onscreen = size.width >= 2 && size.height >= 2 && pos.x + size.width > 0 && pos.y + size.height > 0
      && pos.x < screen.frame.width && pos.y < screen.frame.height
    let title = clean(str(attr(el, kAXTitleAttribute)).isEmpty ? str(attr(el, kAXDescriptionAttribute)) : str(attr(el, kAXTitleAttribute)))
    var value = clean(str(attr(el, kAXValueAttribute)))
    if name == "password" { value = "" }
    if onscreen && (!(title.isEmpty && value.isEmpty) || ["button", "field", "checkbox", "radio", "image", "tab", "slider"].contains(name)) {
      let enabled = (attr(el, kAXEnabledAttribute) as? Bool) ?? true
      let focused = (attr(el, kAXFocusedAttribute) as? Bool) ?? false
      out.append("el\t\(name)\t\(Int(pos.x * scale))\t\(Int(pos.y * scale))\t\(Int(size.width * scale))\t\(Int(size.height * scale))\t\(enabled ? 1 : 0)\t\(focused ? 1 : 0)\t\(title)\t\(value)")
      count += 1
    }
  }
  if let kids = attr(el, kAXChildrenAttribute) as? [AXUIElement] {
    for k in kids { walk(k, depth + 1) }
  }
}

guard let app = NSWorkspace.shared.frontmostApplication else {
  print("note\tno frontmost application")
  exit(0)
}
print("front\t\(clean(app.localizedName ?? ""))")
let ax = AXUIElementCreateApplication(app.processIdentifier)
AXUIElementSetMessagingTimeout(ax, 2.0)
if let wins = attr(ax, kAXWindowsAttribute) as? [AXUIElement] {
  for w in wins { print("window\t\(clean(str(attr(w, kAXTitleAttribute))))"); break }
}
walk(ax, 0)
if truncated && count >= maxEls { print("note\ttruncated at \(maxEls) elements") }
for l in out { print(l) }
