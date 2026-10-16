package app

type Route struct {
	Screen ScreenID
}

type router struct {
	active Route
}

func newRouter() router {
	return router{active: Route{Screen: ScreenDashboard}}
}

func (r router) Active() Route {
	return r.active
}

func registeredScreens() []ScreenID {
	return []ScreenID{ScreenDashboard}
}
