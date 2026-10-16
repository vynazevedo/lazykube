package app

import "github.com/vynazevedo/lazykube/internal/ui"

const maxFrames = 32

type RouteKind int

const (
	RouteScreen RouteKind = iota
	RouteNamespace
	RoutePod
)

type Route struct {
	Kind      RouteKind
	Screen    ScreenID
	Namespace string
	Pod       string
	Node      string
}

type Frame struct {
	Route       Route
	EntryFilter string
	Selected    string
	Focus       Focus
	Filter      string
	NSFilter    string
	PodOffset   int
	NSCursor    int
	DepCursor   int
	Scroll      int
	Follow      bool
	Tab         ui.Tab
	Events      eventsFrame
}

type router struct {
	active      Route
	entryFilter string
	frames      [maxFrames]Frame
	depth       int
}

func newRouter() router {
	return router{active: Route{Kind: RouteScreen, Screen: ScreenDashboard}}
}

func (r router) Active() Route {
	return r.active
}

func (r router) Depth() int {
	return r.depth
}

func (r router) Frame(i int) (Frame, bool) {
	if i < 0 || i >= r.depth {
		return Frame{}, false
	}
	return r.frames[i], true
}

func (r *router) push(current Frame, to Route) bool {
	if to == r.active {
		return false
	}
	if r.depth == maxFrames {
		copy(r.frames[:], r.frames[1:])
		r.frames[maxFrames-1] = Frame{}
		r.depth--
	}
	r.frames[r.depth] = current
	r.depth++
	r.active = to
	return true
}

func (r *router) popTo(depth int) (Frame, bool) {
	if depth < 0 || depth >= r.depth {
		return Frame{}, false
	}
	f := r.frames[depth]
	for i := depth; i < r.depth; i++ {
		r.frames[i] = Frame{}
	}
	r.depth = depth
	r.active = f.Route
	r.entryFilter = f.EntryFilter
	return f, true
}

func (r *router) pop() (Frame, bool) {
	return r.popTo(r.depth - 1)
}

func (r router) trail() []Route {
	routes := make([]Route, 0, r.depth+1)
	for i := 0; i < r.depth; i++ {
		routes = append(routes, r.frames[i].Route)
	}
	return append(routes, r.active)
}

func (r router) crumbStart() int {
	trail := r.trail()
	for i := len(trail) - 1; i >= 0; i-- {
		if trail[i].Kind == RouteScreen {
			return i
		}
	}
	return 0
}

func registeredScreens() []ScreenID {
	return []ScreenID{ScreenDashboard, ScreenWorkloads, ScreenNodes, ScreenEvents}
}
