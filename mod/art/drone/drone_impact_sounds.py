# Generates mod/main/sound/drone/drone_hit_{0,1,2}.wav (light/medium/hard impact):
# a low plastic thump plus inharmonic metallic partials with fast decay, and a
# noise crack. Pure stdlib; run from the repo root: python3 mod/art/drone/drone_impact_sounds.py
import wave, struct, math, random

RATE = 22050
OUT = "mod/main/sound/drone/drone_hit_%d.wav"
# (length s, thump hz, thump gain, metal gain, metal decay, noise gain, peak)
LEVELS = [(0.22, 150, 0.8, 0.30, 38, 0.30, 0.45),
          (0.38, 110, 1.0, 0.55, 26, 0.50, 0.70),
          (0.60,  80, 1.0, 0.80, 17, 0.70, 0.95)]
PARTIALS = [(830, 1.0), (1370, 0.7), (2210, 0.5), (3470, 0.3), (4980, 0.18)]

for lvl, (dur, f0, gt, gm, dm, gn, peak) in enumerate(LEVELS):
    rnd = random.Random(1943 + lvl)
    n = int(dur * RATE)
    lp = 0.0
    buf = []
    for i in range(n):
        t = i / RATE
        thump = math.sin(2 * math.pi * (f0 + 60 * math.exp(-t * 40)) * t) * math.exp(-t * 16)
        metal = sum(a * math.sin(2 * math.pi * f * (1 + 0.01 * lvl) * t) for f, a in PARTIALS) * math.exp(-t * dm) / 2.7
        lp += 0.5 * (rnd.uniform(-1, 1) - lp)
        crack = lp * math.exp(-t * 90)
        buf.append(gt * thump + gm * metal + gn * crack)
    m = max(abs(v) for v in buf)
    fade = int(0.004 * RATE)
    for i in range(fade):
        buf[n - 1 - i] *= i / fade
    w = wave.open(OUT % lvl, "wb")
    w.setnchannels(1); w.setsampwidth(2); w.setframerate(RATE)
    w.writeframes(b"".join(struct.pack("<h", int(32767 * peak * v / m)) for v in buf))
    w.close()
