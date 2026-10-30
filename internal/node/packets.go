package node

// isReplyPacket reports whether a raw IP packet is a reply instead of
// a new call. ICMP echo replies and TCP packets without a lone SYN are
// replies. Everything else counts as a new call.
func isReplyPacket(raw []byte) bool {
	if len(raw) < 20 || raw[0]>>4 != 4 {
		return false
	}
	protocol := raw[9]
	switch protocol {
	case 1:
		return isICMPReply(raw)
	case 6:
		return isTCPReply(raw)
	default:
		return false
	}
}

func isICMPReply(raw []byte) bool {
	if len(raw) < 21 {
		return false
	}
	return raw[20] != 8
}

func isTCPReply(raw []byte) bool {
	if len(raw) < 34 {
		return false
	}
	flags := raw[33]
	const syn, ack = 0x02, 0x10
	return !(flags&syn != 0 && flags&ack == 0)
}
