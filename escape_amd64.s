#include "textflag.h"

DATA ·escape_consts+0(SB)/8,   $0x2222222222222222  // "
DATA ·escape_consts+8(SB)/8,   $0x2222222222222222
DATA ·escape_consts+16(SB)/8,  $0x5C5C5C5C5C5C5C5C  // backslash
DATA ·escape_consts+24(SB)/8,  $0x5C5C5C5C5C5C5C5C
DATA ·escape_consts+32(SB)/8,  $0x3C3C3C3C3C3C3C3C  // <
DATA ·escape_consts+40(SB)/8,  $0x3C3C3C3C3C3C3C3C
DATA ·escape_consts+48(SB)/8,  $0x3E3E3E3E3E3E3E3E  // >
DATA ·escape_consts+56(SB)/8,  $0x3E3E3E3E3E3E3E3E
DATA ·escape_consts+64(SB)/8,  $0x2626262626262626  // &
DATA ·escape_consts+72(SB)/8,  $0x2626262626262626
DATA ·escape_consts+80(SB)/8,  $0xE2E2E2E2E2E2E2E2  // 0xE2 (U+2028/2029 lead)
DATA ·escape_consts+88(SB)/8,  $0xE2E2E2E2E2E2E2E2
DATA ·escape_consts+96(SB)/8,  $0x8080808080808080  // 0x80 (U+2028/2029 second byte)
DATA ·escape_consts+104(SB)/8, $0x8080808080808080
DATA ·escape_consts+112(SB)/8, $0x1F1F1F1F1F1F1F1F  // control threshold
DATA ·escape_consts+120(SB)/8, $0x1F1F1F1F1F1F1F1F
GLOBL ·escape_consts(SB), NOPTR, $128

// func simdFirstEscape(s string) int
//
// Returns the index of the first byte requiring JSON escaping
// (per safeSet: ", \, <, >, &, 0xE2, control <= 0x1F), or len(s).
TEXT ·simdFirstEscape(SB), NOSPLIT, $0-24
	MOVQ s_base+0(FP), SI
	MOVQ s_len+8(FP), CX
	XORL AX, AX              // result index

	CMPQ CX, $16
	JL  byteloop

	PXOR X13, X13            // zero register (held throughout loop)

	MOVOU ·escape_consts+0(SB), X1   // "
	MOVOU ·escape_consts+16(SB), X2  // backslash
	MOVOU ·escape_consts+32(SB), X3  // <
	MOVOU ·escape_consts+48(SB), X4  // >
	MOVOU ·escape_consts+64(SB), X5  // &
	MOVOU ·escape_consts+80(SB), X6  // 0xE2
	MOVOU ·escape_consts+96(SB), X10 // 0x80
	MOVOU ·escape_consts+112(SB), X7 // 0x1F

chunkloop:
	CMPQ CX, $16
	JL  byteloop

	MOVOU (SI), X0           // load 16 bytes (unaligned)
	PXOR X8, X8              // escape-bit accumulator

	MOVO  X0, X9
	PCMPEQB X1, X9          // "
	POR   X9, X8
	MOVO  X0, X9
	PCMPEQB X2, X9          // backslash
	POR   X9, X8
	MOVO  X0, X9
	PCMPEQB X3, X9          // <
	POR   X9, X8
	MOVO  X0, X9
	PCMPEQB X4, X9          // >
	POR   X9, X8
	MOVO  X0, X9
	PCMPEQB X5, X9          // &
	POR   X9, X8
	// only stop at 0xE2 when followed by 0x80
	MOVO  X0, X9
	PCMPEQB X6, X9          // X9 = mask of 0xE2 positions
	MOVO  X0, X11
	PSRLDQ $1, X11          // X11[i] = X0[i+1], X11[15] = 0
	PCMPEQB X10, X11        // X11 = mask of 0x80 at offset+1
	PAND  X9, X11           // X11 = 0xE2 followed by 0x80
	POR   X11, X8

	// control chars (byte <= 0x1F): sat(byte - 0x1F) is 0 exactly when
	// byte <= 0x1F, so PCMPEQB against zero yields the mask.
	MOVO  X0, X9
	PSUBUSB X7, X9          // X9 = sat(byte - 0x1F)
	PCMPEQB X13, X9         // X9 = 0xFF where byte <= 0x1F
	POR   X9, X8

	PMOVMSKB X8, DX         // DX = high-bit mask (16 bits)
	TESTL DX, DX
	JNZ  found

	// if last byte is 0xE2, back up so next chunk can check
	MOVBQZX 15(SI), R8
	CMPB R8, $0xE2
	JE   last_is_e2

	ADDQ $16, SI
	SUBQ $16, CX
	ADDQ $16, AX
	JMP  chunkloop

last_is_e2:
	ADDQ $15, SI
	SUBQ $15, CX
	ADDQ $15, AX
	JMP  chunkloop

found:
	BSFL DX, DX             // lowest set bit -> byte index in block
	ADDQ DX, AX
	MOVQ AX, ret+16(FP)
	RET

byteloop:
	TESTQ CX, CX
	JLE  done
	MOVBQZX (SI), DX
	CMPB DX, $0xE2
	JE   check_e2
	CMPB DX, $0x22
	JE   foundb
	CMPB DX, $0x5C
	JE   foundb
	CMPB DX, $0x3C
	JE   foundb
	CMPB DX, $0x3E
	JE   foundb
	CMPB DX, $0x26
	JE   foundb
	CMPB DX, $0x1F
	JBE  foundb
	INCQ SI
	DECQ CX
	INCQ AX
	JMP  byteloop

	// 0xE2 only flags an escape when followed by 0x80
check_e2:
	CMPQ CX, $2
	JL   skip_e2
	CMPB 1(SI), $0x80
	JE   foundb
skip_e2:
	INCQ SI
	DECQ CX
	INCQ AX
	JMP  byteloop

foundb:
	MOVQ AX, ret+16(FP)
	RET

done:
	MOVQ AX, ret+16(FP)
	RET

// func simdCopySafe(dst []byte, src string) int
//
// Fused scan + copy: loads 16 source bytes into X0, classifies them// with the same PCMPEQB chain as simdFirstEscape, and stores them to
// dst in the same iteration. When an escape is found mid-chunk, the
// full 16 bytes are stored (the extras land in spare capacity and are
// excluded by the caller's slice-length bookkeeping), and the escape
// index is returned. When the last byte of a chunk is 0xE2, only 15
// bytes are committed so the next chunk can re-examine the 0xE2 with
// its lookahead partner.
//
// Frame: $0-48
//   dst_base+0(FP), dst_len+8(FP), dst_cap+16(FP)
//   src_base+24(FP), src_len+32(FP)
//   ret+40(FP)
TEXT ·simdCopySafe(SB), NOSPLIT, $0-48
	MOVQ  src_base+24(FP), SI
	MOVQ  src_len+32(FP), CX
	MOVQ  dst_base+0(FP), DI
	XORL  AX, AX

	CMPQ  CX, $16
	JL    cs_byteloop

	PXOR  X13, X13

	MOVOU ·escape_consts+0(SB), X1
	MOVOU ·escape_consts+16(SB), X2
	MOVOU ·escape_consts+32(SB), X3
	MOVOU ·escape_consts+48(SB), X4
	MOVOU ·escape_consts+64(SB), X5
	MOVOU ·escape_consts+80(SB), X6
	MOVOU ·escape_consts+96(SB), X10
	MOVOU ·escape_consts+112(SB), X7

cs_chunkloop:
	CMPQ  CX, $16
	JL    cs_byteloop

	MOVOU (SI), X0
	PXOR  X8, X8

	MOVO  X0, X9
	PCMPEQB X1, X9
	POR   X9, X8
	MOVO  X0, X9
	PCMPEQB X2, X9
	POR   X9, X8
	MOVO  X0, X9
	PCMPEQB X3, X9
	POR   X9, X8
	MOVO  X0, X9
	PCMPEQB X4, X9
	POR   X9, X8
	MOVO  X0, X9
	PCMPEQB X5, X9
	POR   X9, X8

	MOVO  X0, X9
	PCMPEQB X6, X9
	MOVO  X0, X11
	PSRLDQ $1, X11
	PCMPEQB X10, X11
	PAND  X9, X11
	POR   X11, X8

	MOVO  X0, X9
	PSUBUSB X7, X9
	PCMPEQB X13, X9
	POR   X9, X8

	PMOVMSKB X8, DX
	TESTL DX, DX
	JNZ  cs_found_escape

	MOVBQZX 15(SI), R8
	CMPB   R8, $0xE2
	JE     cs_last_is_e2

	MOVOU  X0, (DI)
	ADDQ   $16, SI
	ADDQ   $16, DI
	SUBQ   $16, CX
	ADDQ   $16, AX
	JMP    cs_chunkloop

cs_last_is_e2:
	MOVOU  X0, (DI)
	ADDQ   $15, SI
	ADDQ   $15, DI
	SUBQ   $15, CX
	ADDQ   $15, AX
	JMP    cs_chunkloop

cs_found_escape:
	BSFL  DX, DX
	MOVOU X0, (DI)
	ADDQ  DX, AX
	MOVQ  AX, ret+40(FP)
	RET

cs_byteloop:
	TESTQ CX, CX
	JLE   cs_done
	MOVBQZX (SI), DX
	CMPB  DX, $0xE2
	JE    cs_check_e2
	CMPB  DX, $0x22
	JE    cs_escape
	CMPB  DX, $0x5C
	JE    cs_escape
	CMPB  DX, $0x3C
	JE    cs_escape
	CMPB  DX, $0x3E
	JE    cs_escape
	CMPB  DX, $0x26
	JE    cs_escape
	CMPB  DX, $0x1F
	JBE   cs_escape
	MOVB  DX, (DI)
	INCQ  SI
	INCQ  DI
	DECQ  CX
	INCQ  AX
	JMP   cs_byteloop

cs_check_e2:
	CMPQ  CX, $2
	JL    cs_safe_e2
	CMPB  1(SI), $0x80
	JE    cs_escape
cs_safe_e2:
	MOVB  DX, (DI)
	INCQ  SI
	INCQ  DI
	DECQ  CX
	INCQ  AX
	JMP   cs_byteloop

cs_escape:
	MOVQ  AX, ret+40(FP)
	RET

cs_done:
	MOVQ  AX, ret+40(FP)
	RET
