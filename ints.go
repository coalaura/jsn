package jsn

const digitPairs = "00010203040506070809" +
	"10111213141516171819" +
	"20212223242526272829" +
	"30313233343536373839" +
	"40414243444546474849" +
	"50515253545556575859" +
	"60616263646566676869" +
	"70717273747576777879" +
	"80818283848586878889" +
	"90919293949596979899"

func appendInt64Fast(b []byte, n int64) []byte {
	if n == 0 {
		return append(b, '0')
	}

	var u uint64

	neg := n < 0

	if neg {
		u = uint64(-n)
	} else {
		u = uint64(n)
	}

	var buf [20]byte

	i := len(buf)

	for u >= 100 {
		q := u / 100
		r := u - q*100

		i -= 2
		buf[i] = digitPairs[2*r]
		buf[i+1] = digitPairs[2*r+1]

		u = q
	}

	if u >= 10 {
		i -= 2
		buf[i] = digitPairs[2*u]
		buf[i+1] = digitPairs[2*u+1]
	} else {
		i--
		buf[i] = digitPairs[2*u+1]
	}

	if neg {
		i--
		buf[i] = '-'
	}

	return append(b, buf[i:]...)
}
