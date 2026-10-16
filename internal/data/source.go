package data

type PodStatus int

const (
	StatusRunning PodStatus = iota
	StatusCompleted
	StatusPending
	StatusContainerCreating
	StatusFailed
	StatusUnknown
)

func (s PodStatus) String() string {
	switch s {
	case StatusRunning:
		return "Running"
	case StatusCompleted:
		return "Completed"
	case StatusPending:
		return "Pending"
	case StatusContainerCreating:
		return "ContainerCreating"
	case StatusFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

type Level int

const (
	LevelInfo Level = iota
	LevelDebug
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

type Cluster struct {
	Context    string
	Version    string
	Nodes      int
	Namespaces int
	Pods       int
	Uptime     string
}

type Node struct {
	Name        string
	Address     string
	Ready       bool
	MemUsed     string
	MemTotal    string
	MemFraction float64
	Pods        int
	PodCapacity int
	Arch        string
}

type Pod struct {
	Name       string
	Namespace  string
	Status     PodStatus
	Restarts   int
	Age        string
	IP         string
	Node       string
	CPURequest int
	CPULimit   int
	MemRequest int
	MemLimit   int
	Image      string
	Controller string
	QoS        string
}

func PodKey(namespace, name string) string {
	return namespace + "/" + name
}

func (p Pod) Key() string {
	return PodKey(p.Namespace, p.Name)
}

type Deployment struct {
	Namespace string
	Name      string
	Ready     int
	Desired   int
}

type Namespace struct {
	Name string
	Pods int
}

type Event struct {
	Age     string
	Type    string
	Reason  string
	Message string
}

type LogLine struct {
	Time    string
	Level   Level
	Message string
}

type Field struct {
	Key   string
	Value string
}

type Snapshot struct {
	Cluster     Cluster
	Node        Node
	Pods        []Pod
	Deployments []Deployment
	Namespaces  []Namespace

	DeploymentCoverage Coverage
}

type Metrics struct {
	Available  bool
	NodeCPU    int
	PodCPU     map[string]int
	CPU        []float64
	CPUNow     int
	Mem        []float64
	MemNow     int
	RX         []float64
	TX         []float64
	RXNow      float64
	TXNow      float64
	HeatLabels []string
	Heat       [][]float64
	MemMax     float64
	RXMax      float64
	TXMax      float64
	Samples    int
	Window     int
	Reason     string
	NetReason  string
	NetStale   bool
}

type Source interface {
	Snapshot() Snapshot
	Metrics(tick int, pod string, heatCols int) Metrics
	Logs(pod string, tick int, n int) []LogLine
	Events(pod string) []Event
	Manifest(pod string) []string
	Describe(pod string) []Field
}

type Live interface {
	Changes() <-chan struct{}
}
