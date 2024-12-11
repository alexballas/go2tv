package uri

// UsesDNSHostValidation returns true if the provided scheme has host validation
// that does not follow RFC3986 (which is quite generic), and assumes a valid
// DNS hostname instead.
//
// This function is declared as a global variable that may be overridden at the package level,
// in case you need specific schemes to validate the host as a DNS name.
//
// See: https://www.iana.org/assignments/uri-schemes/uri-schemes.xhtml
var UsesDNSHostValidation = func(scheme string) bool {
	switch scheme {
	case "dns":
		return true
	case "dntp":
		return true
	case "finger":
		return true
	case "ftp":
		return true
	case "git":
		return true
	case "http":
		return true
	case "https":
		return true
	case "imap":
		return true
	case "irc":
		return true
	case "jms":
		return true
	case "mailto":
		return true
	case "nfs":
		return true
	case "nntp":
		return true
	case "ntp":
		return true
	case "postgres":
		return true
	case "redis":
		return true
	case "rmi":
		return true
	case "rtsp":
		return true
	case "rsync":
		return true
	case "sftp":
		return true
	case "skype":
		return true
	case "smtp":
		return true
	case "snmp":
		return true
	case "soap":
		return true
	case "ssh":
		return true
	case "steam":
		return true
	case "svn":
		return true
	case "tcp":
		return true
	case "telnet":
		return true
	case "udp":
		return true
	case "vnc":
		return true
	case "wais":
		return true
	case "ws":
		return true
	case "wss":
		return true
	}

	return false
}
