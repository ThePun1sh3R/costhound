#!/usr/bin/env python3
"""Generate the README images in docs/assets (run: python3 scripts/readme_assets.py).

Plain SVG with CSS animations (GitHub shows them through <img>; no scripts).
Every animation stops for viewers who ask for reduced motion. Colors follow the
site (accent orange, green for verified savings); dark and light variants are
picked with <picture> in the README.
"""
import os
from html import escape

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "docs", "assets")

THEMES = {
    "dark": dict(text="#e6edf3", muted="#8b949e", accent="#fb923c", good="#4ade80",
                 line="#30363d", panel="#161b22", chip="#21262d"),
    "light": dict(text="#1f2328", muted="#59636e", accent="#c2410c", good="#15803d",
                  line="#d1d9e0", panel="#f6f8fa", chip="#eef1f4"),
}
SANS = "-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif"
MONO = "ui-monospace,SFMono-Regular,Menlo,Consolas,'Liberation Mono',monospace"
REDUCED = "@media (prefers-reduced-motion: reduce){*{animation:none!important}}"


def write(name, body):
    os.makedirs(OUT, exist_ok=True)
    with open(os.path.join(OUT, name), "w") as f:
        f.write(body.strip() + "\n")


# ---------------------------------------------------------------- banner
def banner(t, name):
    chips = [("AWS", "Free, Pro, Team"), ("Azure", "preview"), ("Google Cloud", "preview")]
    x, parts = 0, []
    for i, (label, note) in enumerate(chips):
        w = 28 + 9.8 * len(label) + 8.0 * len(note) + 22
        parts.append(
            f'<g class="chip c{i}" transform="translate({x:.0f},0)">'
            f'<rect width="{w:.0f}" height="34" rx="17" fill="{t["chip"]}" stroke="{t["line"]}"/>'
            f'<circle cx="17" cy="17" r="4" fill="{t["accent"]}"/>'
            f'<text x="28" y="22" font-family="{SANS}" font-size="15" font-weight="600" fill="{t["text"]}">{escape(label)}'
            f'<tspan font-weight="400" fill="{t["muted"]}"> · {escape(note)}</tspan></text></g>')
        x += w + 12
    chips_svg = "".join(parts)
    write(name, f"""
<svg xmlns="http://www.w3.org/2000/svg" width="880" height="230" viewBox="0 0 880 230" role="img" aria-label="costhound: find cloud waste, fix it safely, prove what you saved">
<style>
.mark{{animation:pop .7s ease-out both}}
.sweep{{animation:sweep 2.4s ease-in-out .4s both}}
.tag{{animation:fade .8s ease-out .5s both}}
.chip{{animation:rise .6s ease-out both}}
.c0{{animation-delay:.9s}} .c1{{animation-delay:1.05s}} .c2{{animation-delay:1.2s}}
.coin{{animation:coin 3.2s ease-in-out 1.6s infinite both;transform-origin:62px 62px}}
@keyframes pop{{from{{opacity:0;transform:translateY(8px)}}to{{opacity:1;transform:none}}}}
@keyframes sweep{{from{{transform:scaleX(0)}}to{{transform:scaleX(1)}}}}
@keyframes fade{{from{{opacity:0}}to{{opacity:1}}}}
@keyframes rise{{from{{opacity:0;transform:translateY(10px)}}}}
@keyframes coin{{0%,70%,100%{{transform:none}}80%{{transform:rotate(-12deg)}}90%{{transform:rotate(8deg)}}}}
{REDUCED}
</style>
<g class="mark" transform="translate(0,26)">
  <g class="coin">
    <circle cx="62" cy="62" r="44" fill="none" stroke="{t["accent"]}" stroke-width="6"/>
    <path d="M94 94 L122 122" stroke="{t["accent"]}" stroke-width="10" stroke-linecap="round"/>
    <text x="62" y="80" text-anchor="middle" font-family="{SANS}" font-size="52" font-weight="700" fill="{t["accent"]}">$</text>
  </g>
  <text x="150" y="74" font-family="{SANS}" font-size="64" font-weight="800" letter-spacing="-1.5" fill="{t["text"]}"><tspan fill="{t["accent"]}">cost</tspan>hound</text>
  <rect class="sweep" x="152" y="88" width="300" height="5" rx="2.5" fill="{t["accent"]}" style="transform-origin:152px 90px"/>
  <text class="tag" x="152" y="126" font-family="{SANS}" font-size="21" fill="{t["muted"]}">Find the waste on your cloud bill, fix it safely, prove what you saved.</text>
</g>
<g transform="translate(152,180)">{chips_svg}</g>
</svg>""")


# ---------------------------------------------------------------- terminal demo
DEMO = [  # (text, css class, seconds after start)
    ("$ costhound", "cmd", 0.0),
    ("costhound · AWS waste report", "mut", 1.6),
    ("Account 123456789012 · 17 regions · read-only", "mut", 1.9),
    ("", "", 0),
    ("Estimated waste: $1,248.20 / month  (about $14,978 / year)", "big", 2.6),
    ("", "", 0),
    ("   $584.00/mo  eu-west-1  orders-db", "amt", 3.4),
    ("   RDS on mysql 8.0: extended support, billed per vCPU-hour", "txt", 3.6),
    ("   confidence high · owner data-team (tag owner)", "mut", 3.8),
    ("   fix: upgrade to mysql 8.4 (a review step: it restarts the database)", "fix", 4.0),
    ("", "", 0),
    ("   $250.00/mo  eu-west-1  vol-0a1b2c3d4e5f (old-data)", "amt", 4.7),
    ("   Unattached EBS volume: gp2, 2500 GiB, attached to nothing for 627 days", "txt", 4.9),
    ("   fix: snapshot (backup) → delete → verify; restore from the snapshot to undo", "fix", 5.1),
    ("   ...", "mut", 5.4),
    ("", "", 0),
    ("Verified savings: 3 fix(es) since 2026-10-02, $412.00/mo no longer spent,", "ok", 6.3),
    ("$702.40 saved so far", "ok", 6.4),
]
CYCLE = 14.0


def terminal(t, name):
    lh, top, left = 21, 62, 26
    rows, styles = [], []
    for i, (txt, cls, at) in enumerate(DEMO):
        if not txt:
            continue
        y = top + i * lh
        if cls == "cmd":  # typed, one character at a time
            spans = []
            for j, ch in enumerate(txt):
                k = f"k{i}_{j}"
                p = (0.25 + 0.07 * j) / CYCLE * 100
                styles.append(f"@keyframes {k}{{0%,{p:.2f}%{{opacity:0}}{p + 0.01:.2f}%,94%{{opacity:1}}97%,100%{{opacity:0}}}}"
                              f".{k}{{animation:{k} {CYCLE}s linear infinite both}}")
                fill = t["accent"] if j == 0 else t["text"]
                spans.append(f'<tspan class="{k}" fill="{fill}">{escape(ch)}</tspan>')
            rows.append(f'<text x="{left}" y="{y}" xml:space="preserve">{"".join(spans)}</text>')
            continue
        k = f"l{i}"
        p = at / CYCLE * 100
        styles.append(f"@keyframes {k}{{0%,{p:.2f}%{{opacity:0;transform:translateX(-6px)}}{p + 2:.2f}%,94%{{opacity:1;transform:none}}97%,100%{{opacity:0}}}}"
                      f".{k}{{animation:{k} {CYCLE}s ease-out infinite both}}")
        color = {"mut": t["muted"], "big": t["text"], "amt": t["accent"], "txt": t["text"],
                 "fix": t["muted"], "ok": t["good"]}[cls]
        weight = ' font-weight="700"' if cls in ("big", "amt", "ok") else ""
        body = escape(txt)
        if cls == "fix":
            body = f'<tspan fill="{t["good"]}">fix:</tspan>' + escape(txt.split("fix:", 1)[1])
            body = escape(txt.split("fix:", 1)[0]) + body
        rows.append(f'<text class="{k}" x="{left}" y="{y}" fill="{color}"{weight} xml:space="preserve">{body}</text>')
    h = top + len(DEMO) * lh + 14
    write(name, f"""
<svg xmlns="http://www.w3.org/2000/svg" width="880" height="{h}" viewBox="0 0 880 {h}" role="img" aria-label="Example costhound run: estimated waste per month, two findings with owners and fixes, and the verified savings so far">
<style>
text{{font-family:{MONO};font-size:14.5px}}
{"".join(styles)}
.cur{{animation:blink 1s steps(1) infinite}}
@keyframes blink{{50%{{opacity:0}}}}
{REDUCED}
</style>
<rect x="1" y="1" width="878" height="{h - 2}" rx="12" fill="{t["panel"]}" stroke="{t["line"]}"/>
<circle cx="24" cy="22" r="6" fill="#ff5f57"/><circle cx="44" cy="22" r="6" fill="#febc2e"/><circle cx="64" cy="22" r="6" fill="#28c840"/>
<text x="440" y="27" text-anchor="middle" fill="{t["muted"]}" style="font-family:{SANS};font-size:13px">costhound · example numbers</text>
<line x1="1" y1="40" x2="879" y2="40" stroke="{t["line"]}"/>
{"".join(rows)}
<rect class="cur" x="{left}" y="{top + (len(DEMO) - 1) * lh + 8}" width="9" height="17" fill="{t["accent"]}" opacity=".8"/>
</svg>""")


# ---------------------------------------------------------------- how it works
STEPS = [
    ("Scan", "read-only APIs,", "on your machine"),
    ("Price", "the cloud's own", "price list, today"),
    ("Fix", "a script that backs up,", "verifies, rolls back"),
    ("Prove", "the next scan confirms", "it and counts it once"),
]


def flow(t, name):
    w, h, bw, bh, gap, y0 = 880, 210, 178, 96, 48, 20
    boxes, arrows = [], []
    for i, (title, a, b) in enumerate(STEPS):
        x = i * (bw + gap) + 8
        boxes.append(
            f'<g class="s s{i}"><rect x="{x}" y="{y0}" width="{bw}" height="{bh}" rx="14" fill="{t["panel"]}" stroke="{t["line"]}"/>'
            f'<rect class="glow g{i}" x="{x}" y="{y0}" width="{bw}" height="{bh}" rx="14" fill="none" stroke="{t["accent"]}" stroke-width="2.5"/>'
            f'<text x="{x + 18}" y="{y0 + 34}" font-family="{SANS}" font-size="19" font-weight="700" fill="{t["text"]}">'
            f'<tspan fill="{t["accent"]}">{i + 1}</tspan>  {title}</text>'
            f'<text x="{x + 18}" y="{y0 + 60}" font-family="{SANS}" font-size="13.5" fill="{t["muted"]}">{escape(a)}</text>'
            f'<text x="{x + 18}" y="{y0 + 79}" font-family="{SANS}" font-size="13.5" fill="{t["muted"]}">{escape(b)}</text></g>')
        if i < len(STEPS) - 1:
            ax = x + bw + 8
            arrows.append(f'<path d="M{ax} {y0 + bh / 2} h{gap - 16}" stroke="{t["muted"]}" stroke-width="2" marker-end="url(#ah)"/>')
    last = (len(STEPS) - 1) * (bw + gap) + 8 + bw / 2
    first = 8 + bw / 2
    loop = f"M{last} {y0 + bh + 4} v26 H{first} v-24"
    arrows.append(f'<path d="{loop}" fill="none" stroke="{t["muted"]}" stroke-width="2" stroke-dasharray="5 5" marker-end="url(#ah)"/>')
    arrows.append(f'<text x="{(first + last) / 2}" y="{y0 + bh + 50}" text-anchor="middle" font-family="{SANS}" font-size="13.5" fill="{t["muted"]}">every scan: new waste found, fixed waste proven</text>')
    glow = "".join(
        f"@keyframes gk{i}{{0%,{i * 25}%{{opacity:0}}{i * 25 + 6}%{{opacity:1}}{i * 25 + 20}%,100%{{opacity:0}}}}"
        f".g{i}{{animation:gk{i} 6s ease-in-out infinite both}}" for i in range(len(STEPS)))
    write(name, f"""
<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" role="img" aria-label="How it works: scan read-only, price from the cloud's price list, fix with a script that backs up and rolls back, prove the saving on the next scan">
<defs><marker id="ah" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 z" fill="{t["muted"]}"/></marker></defs>
<style>.glow{{opacity:0}}{glow}{REDUCED}</style>
{"".join(arrows)}{"".join(boxes)}
</svg>""")


# ---------------------------------------------------------------- feature icons
ICONS = {
    "proof": '<path d="M24 4 L40 10 V22 C40 32 33 40 24 44 C15 40 8 32 8 22 V10 Z"/><path d="M16 24 L22 30 L33 18"/>',
    "precise": '<circle cx="24" cy="24" r="16"/><circle cx="24" cy="24" r="7"/><path d="M24 2 V12 M24 36 V46 M2 24 H12 M36 24 H46"/>',
    "undo": '<path d="M14 18 H32 A10 10 0 0 1 32 38 H20"/><path d="M20 10 L12 18 L20 26"/>',
    "owner": '<circle cx="24" cy="16" r="8"/><path d="M8 42 C8 32 15 27 24 27 C33 27 40 32 40 42"/>',
    "local": '<rect x="10" y="21" width="28" height="21" rx="4"/><path d="M16 21 V15 A8 8 0 0 1 32 15 V21"/><circle cx="24" cy="31" r="2.5"/>',
    "clouds": '<path d="M14 36 H36 A8 8 0 0 0 36 20 A12 12 0 0 0 13 18 A9 9 0 0 0 14 36 Z"/><path d="M18 30 H32"/>',
}


def icons():
    for k, p in ICONS.items():
        write(f"icon-{k}.svg", f"""
<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 48 48" fill="none" stroke="#ea580c" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">{p}</svg>""")


for mode, theme in THEMES.items():
    banner(theme, f"banner-{mode}.svg")
    terminal(theme, f"demo-{mode}.svg")
    flow(theme, f"flow-{mode}.svg")
icons()
print("wrote", sorted(os.listdir(OUT)))
