package httpapi

// Server groups the JSON API handlers so a caller can wire every route from a
// single value. The individual handler sets remain usable on their own for
// callers that only need part of the API.
type Server struct {
	Users   *UserHandlers
	Domains *DomainHandlers
	Aliases *AliasHandlers
	Threads *ThreadHandlers
}

// NewServer builds the full JSON API handler set. The user service is shared
// between the /users routes and the domain-scoped user creation route.
func NewServer(users userService, domains domainService, aliases aliasService, threads threadService) *Server {
	return &Server{
		Users:   NewUserHandlers(users),
		Domains: NewDomainHandlers(domains, users),
		Aliases: NewAliasHandlers(aliases),
		Threads: NewThreadHandlers(threads),
	}
}
