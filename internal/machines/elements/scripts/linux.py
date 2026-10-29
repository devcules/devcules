#!/usr/bin/env python3
# walker.py: the focused application's AT-SPI tree as a flat TSV (the
# same shape as the other walkers). Argument 1: max elements. Needs
# python3-gi and at-spi2-core (GNOME/KDE ship both; GTK apps may need
# GTK_MODULES=gtk-frontend-accessibility). Prints a note when the stack
# is missing instead of failing: an empty tree is never a mystery.
import sys

maxn = int(sys.argv[1]) if len(sys.argv) > 1 else 400

try:
    import gi
    gi.require_version("Atspi", "2.0")
    from gi.repository import Atspi
except Exception as e:
    print("note\tno AT-SPI (install python3-gi and at-spi2-core): %s" % e)
    sys.exit(0)

try:
    desktop = Atspi.get_desktop(0)
except Exception as e:
    print("note\tAT-SPI desktop unreachable: %s" % e)
    sys.exit(0)

ROLES = {
    "push button": "button", "toggle button": "button", "menu item": "menuitem",
    "check menu item": "menuitem", "radio menu item": "menuitem",
    "menu": "menu", "menu bar": "menu", "text": "field", "entry": "field",
    "password text": "password", "check box": "checkbox", "radio button": "radio",
    "label": "text", "static": "text", "link": "link", "image": "image",
    "page tab": "tab", "page tab list": "tab", "list item": "row",
    "table cell": "row", "tree item": "row", "frame": "window", "dialog": "window",
    "window": "window", "slider": "slider", "tool bar": "toolbar",
    "list": "list", "table": "list", "tree": "list", "heading": "heading",
    "document web": "web", "combo box": "field", "spin button": "field",
    "paragraph": "text",
}
WANT_EMPTY = {"button", "field", "checkbox", "radio", "image", "tab", "slider"}

def clean(s, n=120):
    s = (s or "").replace("\t", " ").replace("\n", " ").strip()
    return s[:n] if len(s) <= n else s[:n] + "…"

out = []
count = 0
truncated = False

def walk(el, depth):
    global count, truncated
    if count >= maxn or depth > 30:
        truncated = True
        return
    try:
        role = ROLES.get(el.get_role_name(), "")
        if role:
            try:
                ext = el.get_extents()
                x, y, w, h = ext.x, ext.y, ext.width, ext.height
            except Exception:
                x = y = w = h = 0
            onscreen = w >= 2 and h >= 2 and x + w > 0 and y + h > 0
            title = clean(el.get_name() or "")
            value = clean(el.get_text(0, -1) if role in ("field", "text") else (el.get_value() if hasattr(el, "get_value") else ""))
            if role == "password":
                value = ""
            if onscreen and (title or value or role in WANT_EMPTY):
                try:
                    states = el.get_state_set()
                    enabled = not states.contains(Atspi.StateType.ENABLED_INSENSITIVE)
                    focused = states.contains(Atspi.StateType.FOCUSED)
                except Exception:
                    enabled, focused = True, False
                out.append("el\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s" % (
                    role, x, y, w, h, 1 if enabled else 0, 1 if focused else 0, title, value))
                count += 1
        for i in range(el.get_child_count()):
            if count >= maxn:
                truncated = True
                break
            walk(el.get_child_at_index(i), depth + 1)
    except Exception:
        pass  # one dead subtree must not kill the walk

apps = []
for i in range(desktop.get_child_count()):
    a = desktop.get_child_at_index(i)
    try:
        if a.get_state_set().contains(Atspi.StateType.ACTIVE):
            apps.insert(0, a)
        else:
            apps.append(a)
    except Exception:
        apps.append(a)
for a in apps:
    try:
        print("front\t%s" % clean(a.get_name() or ""))
    except Exception:
        pass
    walk(a, 0)
    break  # the focused application only, like the other walkers

if truncated and count >= maxn:
    print("note\ttruncated at %d elements" % maxn)
for l in out:
    print(l)
