B4CONST = [-1, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 16, 32, 64, 128, 256]
B4CONSTU = [32768, 65536, 2, 3, 4, 5, 6, 7, 8, 10, 12, 16, 32, 64, 128, 256]


def s(v, b):
    return v - (1 << b) if v & (1 << (b - 1)) else v


# FLIX format-0xf bundle (64 bits), recovered empirically:
#   slot A = a normal 24-bit core instruction whose op0 field lives at bits 24-27
#            (t@4-7, s@8-11, r@12-15, imm8@16-23 stay where the core ISA puts them)
#   slot B = a branch: r@28-31, s@32-35, signed 18-bit displacement@36-53,
#            opcode@54-63 (bit 54 always 0; index k = bits 55-63), target = pc+4+disp
# k is an index into the branch mnemonics sorted alphabetically, 1-based.
# k is 1-based into the branch mnemonics sorted alphabetically: first the 20 that take
# an r field (register or B4CONST immediate), then the 4 "compare against zero" forms.
# Confirmed: 7=beq, 8=beqi, 0x12=bne, 0x13=bnei, 0x15=beqz, 0x18=bnez.
BRK = [
    None,
    "ball",
    "bany",
    "bbc",
    "bbci",
    "bbs",
    "bbsi",
    "beq",
    "beqi",
    "bge",
    "bgei",
    "bgeu",
    "bgeui",
    "blt",
    "blti",
    "bltu",
    "bltui",
    "bnall",
    "bne",
    "bnei",
    "bnone",
    "beqz",
    "bgez",
    "bltz",
    "bnez",
]
BR_IMM = {"beqi", "bnei", "blti", "bgei"}
BR_IMMU = {"bltui", "bgeui"}
BR_NOR = {"beqz", "bnez", "bltz", "bgez"}

# `ball` and `bany` take a single-bit MASK, not a register: the r field is an
# immediate and the mask is 1 << r.
#
# PROVEN statistically over all 18 images at real instruction boundaries. Every
# genuine register-operand branch avoids a0 (return address) and a1 (SP)
# entirely -- beq (n=888), bgeu (n=621), bltu, bne and bnone all show r in
# 2..15 and NEVER 0 or 1. ball (n=180) and bany (n=171) have r=0 as their most
# common value by a wide margin, with r=1 next. A field that constantly names
# a0/a1 is not a register field.
#
# Confirmed semantically in PROC0's section-state manager, which does the same
# tests with plain 3-byte bit ops on the same byte:
#   0x7ffab265: ball a8,<r=0>  -> fall-through logs StrId 1277 "Crash Dump section is erased"
#   0x7ffab0d9: ball a14,<r=2> -> fall-through logs StrId 1280 "PFail Crash Dump section is erased"
# and the byte is built from the single-bit constants 1, 2, 4, 8.
# So 1<<0 = bit 0 = Crash armed, 1<<2 = bit 2 = PFail armed.
#
# NOTE: bnall and bnone ARE register forms. Do not "fix" those too.
BR_MASK = {"ball", "bany"}


def flix_slotA(q, pc):
    w = (
        ((q >> 24) & 0xF)
        | ((q >> 4) & 0xF) << 4
        | ((q >> 8) & 0xF) << 8
        | ((q >> 12) & 0xF) << 12
        | ((q >> 16) & 0xFF) << 16
    )
    n, t = dis(bytes([w & 0xFF, (w >> 8) & 0xFF, (w >> 16) & 0xFF]), pc, pc)
    return t


def flix_branch(q, pc):
    r = (q >> 28) & 0xF
    s = (q >> 32) & 0xF
    disp = (q >> 36) & 0x3FFFF
    if disp & 0x20000:
        disp -= 0x40000
    k = (q >> 55) & 0x1FF
    tgt = (pc + 4 + disp) & 0xFFFFFFFF
    nm = BRK[k] if k < len(BRK) and BRK[k] else "b?k%02x" % k
    if nm in BR_IMM:
        return "%s a%d,%d,0x%08x" % (nm, s, B4CONST[r], tgt)
    if nm in BR_IMMU:
        return "%s a%d,%d,0x%08x" % (nm, s, B4CONSTU[r], tgt)
    if nm in BR_NOR:
        return "%s a%d,0x%08x" % (nm, s, tgt)
    if nm in BR_MASK:
        return "%s a%d,mask 0x%x,0x%08x" % (nm, s, 1 << r, tgt)
    if nm.startswith("bb"):
        return "%s a%d,b/i%d,0x%08x" % (nm, s, r, tgt)
    return "%s a%d,a%d,0x%08x" % (nm, s, r, tgt)


def dis(d, pc, base):
    """returns (len, text)"""
    o = pc - base
    b0 = d[o]
    op0 = b0 & 0xF
    if 8 <= op0 <= 0xD:  # narrow
        b1 = d[o + 1]
        w = b0 | (b1 << 8)
        t = (w >> 4) & 0xF
        s_ = (w >> 8) & 0xF
        r = (w >> 12) & 0xF
        if op0 == 8:
            return 2, "l32i.n a%d,a%d,0x%x" % (t, s_, r * 4)
        if op0 == 9:
            return 2, "s32i.n a%d,a%d,0x%x" % (t, s_, r * 4)
        if op0 == 0xA:
            return 2, "add.n a%d,a%d,a%d" % (r, s_, t)
        if op0 == 0xB:
            i = r if r else -1
            return 2, "addi.n a%d,a%d,%d" % (t, s_, i)
        if op0 == 0xC:
            if t & 8:
                imm = s(((t & 3) << 4) | r, 6) if False else 0
                imm6 = ((t & 3) << 4) | r
                tgt = pc + 4 + imm6
                return 2, ("beqz.n a%d,0x%08x" % (s_, tgt)) if not (t & 4) else (
                    "bnez.n a%d,0x%08x" % (s_, tgt)
                )
            else:
                imm7 = ((t & 7) << 4) | r
                v = imm7 if imm7 < 0x60 else imm7 - 0x80
                return 2, "movi.n a%d,%d" % (s_, v)
        if op0 == 0xD:
            if r == 0:
                return 2, "mov.n a%d,a%d" % (t, s_)
            if r == 0xF:
                return 2, {
                    0: "ret.n",
                    1: "retw.n",
                    2: "break.n",
                    3: "nop.n",
                    6: "ill.n",
                }.get(t, "d.n?%x" % t)
            return 2, "?narrow d r=%x" % r
        return 2, "?narrow%x" % op0
    if op0 == 0xF:
        q = int.from_bytes(d[o : o + 8], "little")
        return 8, "{ %-32s; %-30s }" % (flix_slotA(q, pc), flix_branch(q, pc))
    if op0 == 0xE:
        q = int.from_bytes(d[o : o + 8], "little")
        pre = (q >> 46) & 3  # slot B class selector
        t = (q >> 28) & 0xF
        if pre == 3:  # j, signed 18-bit displacement at bits 28-45
            disp = (q >> 28) & 0x3FFFF
            if disp & 0x20000:
                disp -= 0x40000
            sB = "j 0x%08x" % ((pc + 4 + disp) & 0xFFFFFFFF)
        elif pre == 2 and ((q >> 44) & 0xF) == 0x8:
            # movi carries a 12-bit immediate, not 8. dest@28-31, imm12@32-43,
            # opcode nibble 0x8 @44-47. The old imm8 rule only matched values
            # below 0x100, which is why StrId-range constants came out as
            # undecoded `?B` -- e.g. `?B 4fdb` is `movi a11,1277`, and 1277 is
            # the base of the crash/pfail section-state string array.
            sB = "movi a%d,%d" % (t, (q >> 32) & 0xFFF)
        elif pre == 2 and ((q >> 40) & 0x3F) == 0x23:
            sB = "mov a%d,a%d" % (t, (q >> 36) & 0xF)
        elif pre == 2 and ((q >> 44) & 0xF) == 0x9:
            # slot-B ALU: [t@28-31][s@32-35][r@36-39][sub@40-43], opcode 0x9.
            # sub 0xE = `or at,as,ar`. Verified at 0x7ffab120, whose result the
            # very next instruction stores with `s32i a11,a5,0x108`.
            sub = (q >> 40) & 0xF
            sr, rr = (q >> 32) & 0xF, (q >> 36) & 0xF
            if sub == 0xE:
                sB = "or a%d,a%d,a%d" % (t, sr, rr)
            else:
                sB = "?Balu sub=%x a%d,a%d,a%d" % (sub, t, sr, rr)
        else:
            sB = "?B %04x" % ((q >> 28) & 0x3FFFF)
        sC = (
            "" if ((q >> 48) & 0xFFFF) == 0xC090 else "; ?C %04x" % ((q >> 48) & 0xFFFF)
        )
        return 8, "{ %-32s; %-22s%s}" % (flix_slotA(q, pc), sB, sC)
    if False:
        q = int.from_bytes(d[o : o + 8], "little")
        imm = d[o + 1] | (d[o + 2] << 8)
        lt = (((pc + 3) & ~3) + (imm << 2) - 0x40000) & 0xFFFFFFFF
        f20 = (d[o + 3] >> 4) | (d[o + 4] << 4) | (d[o + 5] << 12)
        if f20 & 0x80000:
            f20 -= 0x100000
        f12 = (q >> 36) & 0xFFF
        return 8, "FLIX%x op%x %016x  s0l32r=%08x s1j=%08x b12=%08x" % (
            op0,
            d[o] >> 4,
            q,
            lt,
            (pc + 4 + f20) & 0xFFFFFFFF,
            (pc + 4 + f12) & 0xFFFFFFFF,
        )
    w = b0 | (d[o + 1] << 8) | (d[o + 2] << 16)
    t = (w >> 4) & 0xF
    s_ = (w >> 8) & 0xF
    r = (w >> 12) & 0xF
    op1 = (w >> 16) & 0xF
    op2 = (w >> 20) & 0xF
    imm8 = (w >> 16) & 0xFF
    imm12 = (w >> 12) & 0xFFF
    imm16 = (w >> 8) & 0xFFFF
    off18 = (w >> 6) & 0x3FFFF
    if op0 == 0:
        if op1 == 0:
            if op2 == 0:
                if r == 0:
                    m = (t >> 2) & 3
                    n = t & 3
                    if m == 2:
                        return 3, ["ret", "retw", "jx a%d" % s_, "?"][n]
                    if m == 3:
                        return 3, "callx%d a%d" % ([0, 4, 8, 12][n], s_)
                    if m == 0:
                        return 3, "ill"
                    return 3, "snm0?"
                if r == 1:
                    return 3, "movsp a%d,a%d" % (t, s_)
                if r == 2:
                    return 3, "sync/extw"
                if r == 3:
                    return 3, "rfei t=%d s=%d" % (t, s_)
                if r == 4:
                    return 3, "break %d,%d" % (s_, t)
                if r == 5:
                    return 3, "syscall"
                if r == 6:
                    return 3, "rsil a%d,%d" % (t, s_)
                if r == 7:
                    return 3, "waiti %d" % s_
                return 3, "st0 r=%x" % r
            if op2 == 1:
                return 3, "and a%d,a%d,a%d" % (r, s_, t)
            if op2 == 2:
                return 3, "or a%d,a%d,a%d" % (r, s_, t)
            if op2 == 3:
                return 3, "xor a%d,a%d,a%d" % (r, s_, t)
            if op2 == 4:
                if r == 4:
                    return 3, "ssai %d" % (s_ | ((t & 1) << 4))
                return 3, {
                    0: "ssr a%d" % s_,
                    1: "ssl a%d" % s_,
                    2: "ssa8l a%d" % s_,
                    3: "ssa8b a%d" % s_,
                    8: "rotw %d" % s(t, 4),
                    0xE: "nsa a%d,a%d" % (t, s_),
                    0xF: "nsau a%d,a%d" % (t, s_),
                }.get(r, "st1 r=%x" % r)
            if op2 == 6:
                return 3, {0: "neg a%d,a%d" % (r, t), 1: "abs a%d,a%d" % (r, t)}.get(
                    s_, "rt0"
                )
            if op2 >= 8:
                nm = [
                    "add",
                    "addx2",
                    "addx4",
                    "addx8",
                    "sub",
                    "subx2",
                    "subx4",
                    "subx8",
                ][op2 - 8]
                return 3, "%s a%d,a%d,a%d" % (nm, r, s_, t)
            return 3, "rst0 op2=%x" % op2
        if op1 == 1:
            if op2 in (0, 1):
                return 3, "slli a%d,a%d,%d" % (r, s_, 32 - (((op2 & 1) << 4) | t))
            if op2 in (2, 3):
                return 3, "srai a%d,a%d,%d" % (r, t, (((op2 & 1) << 4) | s_))
            if op2 == 4:
                return 3, "srli a%d,a%d,%d" % (r, t, s_)
            if op2 == 6:
                return 3, "xsr a%d,%d" % (t, (r << 4) | s_)
            if op2 == 8:
                return 3, "src a%d,a%d,a%d" % (r, s_, t)
            if op2 == 9:
                return 3, "srl a%d,a%d" % (r, t)
            if op2 == 0xA:
                return 3, "sll a%d,a%d" % (r, s_)
            if op2 == 0xB:
                return 3, "sra a%d,a%d" % (r, t)
            if op2 == 0xC:
                return 3, "mul16u a%d,a%d,a%d" % (r, s_, t)
            if op2 == 0xD:
                return 3, "mul16s a%d,a%d,a%d" % (r, s_, t)
            return 3, "rst1 op2=%x" % op2
        if op1 == 2:
            nm = {
                0xF: "rems",
                0xE: "remu",
                0xD: "quos",
                0xC: "quou",
                8: "mull",
                0xA: "muluh",
                0xB: "mulsh",
            }.get(op2, "rst2_%x" % op2)
            return 3, "%s a%d,a%d,a%d" % (nm, r, s_, t)
        if op1 == 3:
            if op2 == 0:
                return 3, "rsr a%d,%d" % (t, (r << 4) | s_)
            if op2 == 1:
                return 3, "wsr a%d,%d" % (t, (r << 4) | s_)
            if op2 == 2:
                return 3, "sext a%d,a%d,%d" % (r, s_, t + 7)
            if op2 == 3:
                return 3, "clamps a%d,a%d,%d" % (r, s_, t + 7)
            if op2 in (4, 5, 6, 7):
                return 3, "%s a%d,a%d,a%d" % (
                    ["min", "max", "minu", "maxu"][op2 - 4],
                    r,
                    s_,
                    t,
                )
            if op2 in (8, 9, 0xA, 0xB):
                return 3, "%s a%d,a%d,a%d" % (
                    ["moveqz", "movnez", "movltz", "movgez"][op2 - 8],
                    r,
                    s_,
                    t,
                )
            if op2 == 0xC:
                return 3, "movf a%d,a%d,b%d" % (r, s_, t)
            if op2 == 0xD:
                return 3, "movt a%d,a%d,b%d" % (r, s_, t)
            if op2 == 0xE:
                return 3, "rur a%d,%d" % (r, (s_ << 4) | t)
            if op2 == 0xF:
                return 3, "wur a%d,%d" % (t, (s_ << 4) | r)
        if op1 in (4, 5):
            sa = s_ | ((op1 & 1) << 4)
            return 3, "extui a%d,a%d,%d,%d" % (r, t, sa, op2 + 1)
        return 3, "qrst op1=%x op2=%x" % (op1, op2)
    if op0 == 1:
        tgt = ((pc + 3) & ~3) + (imm16 << 2) - 0x40000
        return 3, "l32r a%d,0x%08x" % (t, tgt & 0xFFFFFFFF)
    if op0 == 2:
        if r == 0:
            return 3, "l8ui a%d,a%d,0x%x" % (t, s_, imm8)
        if r == 1:
            return 3, "l16ui a%d,a%d,0x%x" % (t, s_, imm8 * 2)
        if r == 2:
            return 3, "l32i a%d,a%d,0x%x" % (t, s_, imm8 * 4)
        if r == 4:
            return 3, "s8i a%d,a%d,0x%x" % (t, s_, imm8)
        if r == 5:
            return 3, "s16i a%d,a%d,0x%x" % (t, s_, imm8 * 2)
        if r == 6:
            return 3, "s32i a%d,a%d,0x%x" % (t, s_, imm8 * 4)
        if r == 9:
            return 3, "l16si a%d,a%d,0x%x" % (t, s_, imm8 * 2)
        if r == 0xA:
            return 3, "movi a%d,%d" % (t, s((s_ << 8) | imm8, 12))
        if r == 0xB:
            return 3, "l32ai a%d,a%d,0x%x" % (t, s_, imm8 * 4)
        if r == 0xC:
            return 3, "addi a%d,a%d,%d" % (t, s_, s(imm8, 8))
        if r == 0xD:
            return 3, "addmi a%d,a%d,%d" % (t, s_, s(imm8, 8) * 256)
        if r == 0xE:
            return 3, "s32c1i a%d,a%d,0x%x" % (t, s_, imm8 * 4)
        if r == 0xF:
            return 3, "s32ri a%d,a%d,0x%x" % (t, s_, imm8 * 4)
        if r == 7:
            return 3, "cache r=%x t=%x" % (r, t)
        return 3, "lsai r=%x" % r
    if op0 == 5:
        n = (w >> 4) & 3
        tgt = ((pc & ~3) + (s(off18, 18) << 2) + 4) & 0xFFFFFFFF
        return 3, "call%d 0x%08x" % ([0, 4, 8, 12][n], tgt)
    if op0 == 6:
        n = (w >> 4) & 3
        m = (w >> 6) & 3
        if n == 0:
            return 3, "j 0x%08x" % ((pc + 4 + s(off18, 18)) & 0xFFFFFFFF)
        if n == 1:
            nm = ["beqz", "bnez", "bltz", "bgez"][m]
            return 3, "%s a%d,0x%08x" % (nm, s_, (pc + 4 + s(imm12, 12)) & 0xFFFFFFFF)
        if n == 2:
            nm = ["beqi", "bnei", "blti", "bgei"][m]
            return 3, "%s a%d,%d,0x%08x" % (
                nm,
                s_,
                B4CONST[r],
                (pc + 4 + s(imm8, 8)) & 0xFFFFFFFF,
            )
        if n == 3:
            if m == 0:
                return 3, "entry a%d,0x%x" % (s_, imm12 * 8)
            if m == 1:
                if r == 0:
                    return 3, "bf b%d,0x%08x" % (s_, (pc + 4 + s(imm8, 8)) & 0xFFFFFFFF)
                if r == 1:
                    return 3, "bt b%d,0x%08x" % (s_, (pc + 4 + s(imm8, 8)) & 0xFFFFFFFF)
                if r == 8:
                    return 3, "loop a%d,0x%08x" % (s_, (pc + 4 + imm8) & 0xFFFFFFFF)
                if r == 9:
                    return 3, "loopnez a%d,0x%08x" % (s_, (pc + 4 + imm8) & 0xFFFFFFFF)
                if r == 0xA:
                    return 3, "loopgtz a%d,0x%08x" % (s_, (pc + 4 + imm8) & 0xFFFFFFFF)
                return 3, "b1 r=%x" % r
            if m == 2:
                return 3, "bltui a%d,%d,0x%08x" % (
                    s_,
                    B4CONSTU[r],
                    (pc + 4 + s(imm8, 8)) & 0xFFFFFFFF,
                )
            if m == 3:
                return 3, "bgeui a%d,%d,0x%08x" % (
                    s_,
                    B4CONSTU[r],
                    (pc + 4 + s(imm8, 8)) & 0xFFFFFFFF,
                )
    if op0 == 7:
        tgt = (pc + 4 + s(imm8, 8)) & 0xFFFFFFFF
        if r in (6, 7):
            return 3, "bbci a%d,%d,0x%08x" % (s_, ((r & 1) << 4) | t, tgt)
        if r in (0xE, 0xF):
            return 3, "bbsi a%d,%d,0x%08x" % (s_, ((r & 1) << 4) | t, tgt)
        nm = {
            0: "bnone",
            1: "beq",
            2: "blt",
            3: "bltu",
            4: "ball",
            5: "bbc",
            8: "bany",
            9: "bne",
            0xA: "bge",
            0xB: "bgeu",
            0xC: "bnall",
            0xD: "bbs",
        }.get(r, "b?%x" % r)
        return 3, "%s a%d,a%d,0x%08x" % (nm, s_, t, tgt)

    return 3, "op0=%x ???" % op0


def run(d, base, start, end, strs=None):
    pc = start
    out = []
    while pc < end:
        try:
            n, txt = dis(d, pc, base)
        except Exception as e:
            n, txt = 3, "ERR " + str(e)
        raw = " ".join("%02x" % b for b in d[pc - base : pc - base + n])
        ann = ""
        if txt.startswith("l32r") and strs:
            a = int(txt.split("0x")[-1], 16)
            if a in strs:
                ann = "   ; " + strs[a]
        out.append("%08x: %-9s %-34s%s" % (pc, raw, txt, ann))
        pc += n
    return "\n".join(out)
