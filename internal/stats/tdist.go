package stats

import "math"

func studentTCDF(t, df float64) float64 {
	tail := 0.5 * regularizedIncompleteBeta(df/2, 0.5, df/(df+t*t))
	if t >= 0 {
		return 1 - tail
	}
	return tail
}

func twoTailedPValue(t, df float64) float64 {
	return regularizedIncompleteBeta(df/2, 0.5, df/(df+t*t))
}

func studentTQuantile(p, df float64) float64 {
	if p == 0.5 {
		return 0
	}
	if p < 0.5 {
		return -studentTQuantile(1-p, df)
	}
	hi := 1.0
	for studentTCDF(hi, df) < p && hi < 1e12 {
		hi *= 2
	}
	lo := 0.0
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		if studentTCDF(mid, df) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func regularizedIncompleteBeta(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lgab, _ := math.Lgamma(a + b)
	lga, _ := math.Lgamma(a)
	lgb, _ := math.Lgamma(b)
	front := math.Exp(lgab - lga - lgb + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(a, b, x) / a
	}
	return 1 - front*betaContinuedFraction(b, a, 1-x)/b
}

func betaContinuedFraction(a, b, x float64) float64 {
	const maxIterations = 300
	const epsilon = 3e-14
	const tiny = 1e-300
	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIterations; m++ {
		fm := float64(m)
		fm2 := float64(2 * m)
		numer := fm * (b - fm) * x / ((qam + fm2) * (a + fm2))
		d = 1 + numer*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + numer/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		numer = -(a + fm) * (qab + fm) * x / ((a + fm2) * (qap + fm2))
		d = 1 + numer*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + numer/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < epsilon {
			break
		}
	}
	return h
}
