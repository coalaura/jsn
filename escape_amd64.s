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
DATA ·escape_consts+80(SB)/8,  $0x2020202020202020  // signed control/non-ASCII threshold
DATA ·escape_consts+88(SB)/8,  $0x2020202020202020
GLOBL ·escape_consts(SB), NOPTR, $96

// func simdFirstEscape(s string) int
//
// Returns the index of the first byte requiring JSON escaping
// (per safeSet: ", \, <, >, &, non-ASCII, control <= 0x1F), or len(s).
TEXT ·simdFirstEscape(SB), NOSPLIT, $0-24
	MOVQ s_base+0(FP), SI
	MOVQ s_len+8(FP), CX
	XORL AX, AX              // result index

	CMPQ CX, $16
	JL  byteloop

	MOVOU ·escape_consts+0(SB), X1   // "
	MOVOU ·escape_consts+16(SB), X2  // backslash
	MOVOU ·escape_consts+32(SB), X3  // <
	MOVOU ·escape_consts+48(SB), X4  // >
	MOVOU ·escape_consts+64(SB), X5  // &
	MOVOU ·escape_consts+80(SB), X7 // 0x20

chunkloop:
	CMPQ CX, $16
	JL  byteloop

	MOVOU (SI), X0           // load 16 bytes (unaligned)
	// Signed 0x20 > byte catches both controls and non-ASCII bytes.
	MOVO X7, X8
	PCMPGTB X0, X8

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

	PMOVMSKB X8, DX         // DX = high-bit mask (16 bits)
	TESTL DX, DX
	JNZ  found

	ADDQ $16, SI
	SUBQ $16, CX
	ADDQ $16, AX
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
	CMPB DX, $0x80
	JAE  foundb
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

foundb:
	MOVQ AX, ret+16(FP)
	RET

done:
	MOVQ AX, ret+16(FP)
	RET

// func simdCopySafe(dst []byte, src string) int
//
// Fused scan + copy: loads 16 source bytes into X0, classifies them
// with the same PCMPEQB chain as simdFirstEscape, and stores them to
// dst in the same iteration. When an escape is found mid-chunk, the
// full 16 bytes are stored (the extras land in spare capacity and are
// excluded by the caller's slice-length bookkeeping), and the escape
// index is returned. Non-ASCII bytes are validated by the Go caller.
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

	MOVOU ·escape_consts+0(SB), X1
	MOVOU ·escape_consts+16(SB), X2
	MOVOU ·escape_consts+32(SB), X3
	MOVOU ·escape_consts+48(SB), X4
	MOVOU ·escape_consts+64(SB), X5
	MOVOU ·escape_consts+80(SB), X7

cs_chunkloop:
	CMPQ  CX, $16
	JL    cs_byteloop

	MOVOU (SI), X0
	MOVO X7, X8
	PCMPGTB X0, X8

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

	PMOVMSKB X8, DX
	TESTL DX, DX
	JNZ  cs_found_escape

	MOVOU  X0, (DI)
	ADDQ   $16, SI
	ADDQ   $16, DI
	SUBQ   $16, CX
	ADDQ   $16, AX
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
	CMPB  DX, $0x80
	JAE   cs_escape
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

cs_escape:
	MOVQ  AX, ret+40(FP)
	RET

cs_done:
	MOVQ  AX, ret+40(FP)
	RET
