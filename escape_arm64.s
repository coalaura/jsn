#include "textflag.h"

// Both helpers classify complete 16-byte blocks with baseline ARM64 NEON.
// Signed byte comparison against 0x20 catches controls and all non-ASCII;
// the Go caller validates UTF-8 after either helper stops.
#define INIT_ESCAPE_CONSTANTS \
	MOVD $0x22, R4; \
	VMOV R4, V1.B16; \
	MOVD $0x5c, R4; \
	VMOV R4, V2.B16; \
	MOVD $0x3c, R4; \
	VMOV R4, V3.B16; \
	MOVD $0x3e, R4; \
	VMOV R4, V4.B16; \
	MOVD $0x26, R4; \
	VMOV R4, V5.B16; \
	MOVD $0x20, R4; \
	VMOV R4, V6.B16

// Produce four bits per input byte in R4, ordered from least significant.
// Each comparison lane is 0x00 or 0xff, so narrowing adjacent byte pairs
// with a four-bit shift preserves one nibble for each byte.
#define ESCAPE_MASK \
	VCMGT V0.B16, V6.B16, V7.B16; \
	VCMEQ V1.B16, V0.B16, V8.B16; \
	VORR V8.B16, V7.B16, V7.B16; \
	VCMEQ V2.B16, V0.B16, V8.B16; \
	VORR V8.B16, V7.B16, V7.B16; \
	VCMEQ V3.B16, V0.B16, V8.B16; \
	VORR V8.B16, V7.B16, V7.B16; \
	VCMEQ V4.B16, V0.B16, V8.B16; \
	VORR V8.B16, V7.B16, V7.B16; \
	VCMEQ V5.B16, V0.B16, V8.B16; \
	VORR V8.B16, V7.B16, V7.B16; \
	VSHRN $4, V7.H8, V7.B8; \
	VMOV V7.D[0], R4

// func simdFirstEscape(s string) int
TEXT ·simdFirstEscape(SB), NOSPLIT, $0-24
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD ZR, R2
	CMP $16, R1
	BLT scan_tail

	INIT_ESCAPE_CONSTANTS

scan_loop:
	// Never read beyond the source, including at page boundaries.
	VLD1 (R0), [V0.B16]
	ESCAPE_MASK
	CBNZ R4, scan_found
	ADD $16, R0
	ADD $16, R2
	SUB $16, R1
	CMP $16, R1
	BGE scan_loop

scan_tail:
	CBZ R1, scan_done
	MOVBU (R0), R4
	CMP $0x20, R4
	BLO scan_done
	CMP $0x80, R4
	BHS scan_done
	CMP $0x22, R4
	BEQ scan_done
	CMP $0x5c, R4
	BEQ scan_done
	CMP $0x3c, R4
	BEQ scan_done
	CMP $0x3e, R4
	BEQ scan_done
	CMP $0x26, R4
	BEQ scan_done
	ADD $1, R0
	ADD $1, R2
	SUB $1, R1
	B scan_tail

scan_found:
	RBIT R4, R4
	CLZ R4, R4
	ADD R4>>2, R2, R2

scan_done:
	MOVD R2, ret+16(FP)
	RET

// func simdCopySafe(dst []byte, src string) int
// The caller provides at least len(src) writable bytes. As on amd64, a
// matching block is stored in full; only the returned safe prefix is used.
TEXT ·simdCopySafe(SB), NOSPLIT, $0-48
	MOVD src_base+24(FP), R0
	MOVD src_len+32(FP), R1
	MOVD ZR, R2
	MOVD dst_base+0(FP), R3
	CMP $16, R1
	BLT copy_tail

	INIT_ESCAPE_CONSTANTS

copy_loop:
	VLD1 (R0), [V0.B16]
	ESCAPE_MASK
	VST1 [V0.B16], (R3)
	CBNZ R4, copy_found
	ADD $16, R0
	ADD $16, R3
	ADD $16, R2
	SUB $16, R1
	CMP $16, R1
	BGE copy_loop

copy_tail:
	CBZ R1, copy_done
	MOVBU (R0), R4
	CMP $0x20, R4
	BLO copy_done
	CMP $0x80, R4
	BHS copy_done
	CMP $0x22, R4
	BEQ copy_done
	CMP $0x5c, R4
	BEQ copy_done
	CMP $0x3c, R4
	BEQ copy_done
	CMP $0x3e, R4
	BEQ copy_done
	CMP $0x26, R4
	BEQ copy_done
	MOVB R4, (R3)
	ADD $1, R0
	ADD $1, R3
	ADD $1, R2
	SUB $1, R1
	B copy_tail

copy_found:
	RBIT R4, R4
	CLZ R4, R4
	ADD R4>>2, R2, R2

copy_done:
	MOVD R2, ret+40(FP)
	RET
