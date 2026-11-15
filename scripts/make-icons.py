#!/usr/bin/env python3
"""Builds every icon file from packaging/icon/icon.svg (needs rsvg-convert).

  packaging/icon/AppIcon.icns            Mac app icon (copied into the .app by build.sh)
  packaging/icon/AppIcon.ico             Windows icon
  cmd/sstv-console/rsrc_windows_amd64.syso   the .ico as a resource: the Go linker puts it in SSTV-Console.exe
  cmd/sstv-console/web/favicon.svg       the page's icon

No other tools: the .icns, .ico and .syso (a COFF object holding a resource tree) are written here.
Run it again after changing icon.svg and commit the results.
"""
import struct, subprocess, shutil, pathlib

root = pathlib.Path(__file__).resolve().parent.parent
icon = root / "packaging/icon"
svg = icon / "icon.svg"

def png(size):
    return subprocess.run(["rsvg-convert", "-w", str(size), "-h", str(size), str(svg)], check=True, capture_output=True).stdout

# --- Mac: .icns, PNG-compressed entries
mac = [("icp4", 16), ("icp5", 32), ("icp6", 64), ("ic07", 128), ("ic08", 256), ("ic09", 512), ("ic10", 1024)]
body = b""
for tag, size in mac:
    d = png(size)
    body += tag.encode() + struct.pack(">I", 8 + len(d)) + d
(icon / "AppIcon.icns").write_bytes(b"icns" + struct.pack(">I", 8 + len(body)) + body)

# --- Windows: .ico, PNG-compressed entries (Windows Vista and later)
sizes = [16, 24, 32, 48, 64, 128, 256]
imgs = [png(s) for s in sizes]
head = struct.pack("<HHH", 0, 1, len(sizes))
off = 6 + 16 * len(sizes)
ents = b""
for s, d in zip(sizes, imgs):
    ents += struct.pack("<BBBBHHII", s % 256, s % 256, 0, 0, 1, 32, len(d), off)
    off += len(d)
(icon / "AppIcon.ico").write_bytes(head + ents + b"".join(imgs))

# --- Windows: the same icons as a COFF resource object (RT_ICON per size + RT_GROUP_ICON)
RT_ICON, RT_GROUP_ICON, LANG = 3, 14, 0x0409
group = struct.pack("<HHH", 0, 1, len(sizes))
for i, (s, d) in enumerate(zip(sizes, imgs)):
    group += struct.pack("<BBBBHHIH", s % 256, s % 256, 0, 0, 1, 32, len(d), i + 1)
blobs = [(RT_ICON, i + 1, d) for i, d in enumerate(imgs)] + [(RT_GROUP_ICON, 1, group)]

# layout: root dir | type dirs | name dirs | language dirs | data entries | data
def dir_size(n): return 16 + 8 * n
types = [RT_ICON, RT_GROUP_ICON]
by_type = {t: [b for b in blobs if b[0] == t] for t in types}
pos = dir_size(len(types))
type_off = {}
for t in types:
    type_off[t] = pos; pos += dir_size(len(by_type[t]))
name_off = {}
for t in types:
    for b in by_type[t]:
        name_off[(t, b[1])] = pos; pos += dir_size(1)
entry_off = {}
for t in types:
    for b in by_type[t]:
        entry_off[(t, b[1])] = pos; pos += 16
data_off = {}
for t in types:
    for b in by_type[t]:
        pos = (pos + 7) & ~7
        data_off[(t, b[1])] = pos; pos += len(b[2])
rsrc = bytearray(pos)
def put(o, fmt, *a): struct.pack_into(fmt, rsrc, o, *a)
SUB = 0x80000000
put(0, "<IIHHHH", 0, 0, 0, 0, 0, len(types))
for i, t in enumerate(types):
    put(16 + 8 * i, "<II", t, SUB | type_off[t])
    o = type_off[t]
    put(o, "<IIHHHH", 0, 0, 0, 0, 0, len(by_type[t]))
    for j, b in enumerate(by_type[t]):
        put(o + 16 + 8 * j, "<II", b[1], SUB | name_off[(t, b[1])])
        no = name_off[(t, b[1])]
        put(no, "<IIHHHH", 0, 0, 0, 0, 0, 1)
        put(no + 16, "<II", LANG, entry_off[(t, b[1])])
        eo = entry_off[(t, b[1])]
        put(eo, "<IIII", data_off[(t, b[1])], len(b[2]), 0, 0)  # first field: RVA, fixed up by a relocation
        rsrc[data_off[(t, b[1])]:data_off[(t, b[1])] + len(b[2])] = b[2]
relocs = b"".join(struct.pack("<IIH", eo, 0, 3) for eo in entry_off.values())  # IMAGE_REL_AMD64_ADDR32NB against .rsrc
raw_ptr = 20 + 40
reloc_ptr = raw_ptr + len(rsrc)
sym_ptr = reloc_ptr + len(relocs)
coff = struct.pack("<HHIIIHH", 0x8664, 1, 0, sym_ptr, 1, 0, 0)
coff += struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(rsrc), raw_ptr, reloc_ptr, 0, len(entry_off), 0, 0xC0300040)
coff += bytes(rsrc) + relocs
coff += struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0) + struct.pack("<I", 4)
(root / "cmd/sstv-console/rsrc_windows_amd64.syso").write_bytes(coff)

# --- the page's icon
shutil.copy(svg, root / "cmd/sstv-console/web/favicon.svg")
print("icons written")
