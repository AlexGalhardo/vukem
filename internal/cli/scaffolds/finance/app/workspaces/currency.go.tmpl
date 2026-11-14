package workspaces

import "strings"

// iso4217 lists the active ISO 4217 alphabetic codes a workspace may use. The
// check is a lookup, not a three-letter pattern, so ZZZ is refused.
var iso4217 = func() map[string]bool {
	codes := strings.Fields(`AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BRL BSD BTN
		BWP BYN BZD CAD CDF CHF CLP CNY COP CRC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP
		GMD GNF GTQ GYD HKD HNL HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK
		LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB
		PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB
		TJS TMT TND TOP TRY TTD TWD TZS UAH UGX USD UYU UZS VES VND VUV WST XAF XCD XOF XPF YER ZAR ZMW ZWG`)

	set := make(map[string]bool, len(codes))
	for _, c := range codes {
		set[c] = true
	}

	return set
}()

// validCurrency reports whether code (already upper-cased) is an ISO 4217 code.
func validCurrency(code string) bool { return iso4217[code] }
