package uri

// Builder builds URIs.
type Builder interface {
	URI() URI
	SetScheme(scheme string) Builder
	SetUserInfo(userinfo string) Builder
	SetHost(host string) Builder
	SetPort(port string) Builder
	SetPath(path string) Builder
	SetQuery(query string) Builder
	SetFragment(fragment string) Builder

	// Returns the URI this Builder represents.
	String() string
}

func (u *uri) SetScheme(scheme string) Builder {
	u.scheme = scheme
	return u
}

func (u *uri) SetUserInfo(userinfo string) Builder {
	u.ensureAuthorityExists()
	u.authority.userinfo = userinfo
	return u
}

func (u *uri) SetHost(host string) Builder {
	u.ensureAuthorityExists()
	u.authority.host = host
	return u
}

func (u *uri) SetPort(port string) Builder {
	u.ensureAuthorityExists()
	u.authority.port = port
	return u
}

func (u *uri) SetPath(path string) Builder {
	u.ensureAuthorityExists()
	u.authority.path = path
	return u
}

func (u *uri) SetQuery(query string) Builder {
	u.query = query
	return u
}

func (u *uri) SetFragment(fragment string) Builder {
	u.fragment = fragment
	return u
}

func (u *uri) Builder() Builder {
	return u
}
