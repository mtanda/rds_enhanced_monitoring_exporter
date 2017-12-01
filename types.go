package main

import "strings"

type RDSOSMetrics struct {
	CpuUtilization     CpuUtilization    `json:"cpuUtilization"`
	DiskIO             []DiskIO          `json:"diskIO"`
	Engine             string            `json:"engine"`
	FileSys            []FileSys         `json:"fileSys"`
	InstanceID         string            `json:"instanceID"`
	InstanceResourceID string            `json:"instanceResourceID"`
	LoadAverageMinute  LoadAverageMinute `json:"loadAverageMinute"`
	Memory             Memory            `json:"memory"`
	Network            []Network         `json:"network"`
	NumVCPUs           float64           `json:"numVCPUs"`
	Swap               Swap              `json:"swap"`
	Tasks              Tasks             `json:"tasks"`
	Timestamp          string            `json:"timestamp"`
	Uptime             string            `json:"uptime"`
	Version            float64           `json:"version"`
}

type CpuUtilization struct {
	Guest  float64 `json:"guest"`
	Idle   float64 `json:"idle"`
	Irq    float64 `json:"irq"`
	Nice   float64 `json:"nice"`
	Steal  float64 `json:"steal"`
	System float64 `json:"system"`
	Total  float64 `json:"total"`
	User   float64 `json:"user"`
	Wait   float64 `json:"wait"`
}

type DiskIO struct {
	AvgQueueLen float64 `json:"avgQueueLen"`
	AvgReqSz    float64 `json:"avgReqSz"`
	Await       float64 `json:"await"`
	Device      string  `json:"device"`
	ReadIOsPS   float64 `json:"readIOsPS"`
	ReadKb      float64 `json:"readKb"`
	ReadKbPS    float64 `json:"readKbPS"`
	RrqmPS      float64 `json:"rrqmPS"`
	Tps         float64 `json:"tps"`
	Util        float64 `json:"util"`
	WriteIOsPS  float64 `json:"writeIOsPS"`
	WriteKb     float64 `json:"writeKb"`
	WriteKbPS   float64 `json:"writeKbPS"`
	WrqmPS      float64 `json:"wrqmPS"`
}

type FileSys struct {
	MaxFiles        float64 `json:"maxFiles"`
	MountPoint      string  `json:"mountPoint"`
	Name            string  `json:"name"`
	Total           float64 `json:"total"`
	Used            float64 `json:"used"`
	UsedFilePercent float64 `json:"usedFilePercent"`
	UsedFiles       float64 `json:"usedFiles"`
	UsedPercent     float64 `json:"usedPercent"`
}

type LoadAverageMinute struct {
	Fifteen float64 `json:"fifteen"`
	Five    float64 `json:"five"`
	One     float64 `json:"one"`
}

type Memory struct {
	Active         float64 `json:"active"`
	Buffers        float64 `json:"buffers"`
	Cached         float64 `json:"cached"`
	Dirty          float64 `json:"dirty"`
	Free           float64 `json:"free"`
	HugePagesFree  float64 `json:"hugePagesFree"`
	HugePagesRsvd  float64 `json:"hugePagesRsvd"`
	HugePagesSize  float64 `json:"hugePagesSize"`
	HugePagesSurp  float64 `json:"hugePagesSurp"`
	HugePagesTotal float64 `json:"hugePagesTotal"`
	Inactive       float64 `json:"inactive"`
	Mapped         float64 `json:"mapped"`
	PageTables     float64 `json:"pageTables"`
	Slab           float64 `json:"slab"`
	Total          float64 `json:"total"`
	Writeback      float64 `json:"writeback"`
}

type Network struct {
	Interface string  `json:"interface"`
	Rx        float64 `json:"rx"`
	Tx        float64 `json:"tx"`
}
type Swap struct {
	Cached float64 `json:"cached"`
	Free   float64 `json:"free"`
	In     float64 `json:"in"`
	Out    float64 `json:"out"`
	Total  float64 `json:"total"`
}
type Tasks struct {
	Blocked  float64 `json:"blocked"`
	Running  float64 `json:"running"`
	Sleeping float64 `json:"sleeping"`
	Stopped  float64 `json:"stopped"`
	Total    float64 `json:"total"`
	Zombie   float64 `json:"zombie"`
}

type Labels map[string]string

func (l Labels) String() string {
	r := make([]string, 0)
	for k, v := range l {
		r = append(r, k+"=\""+v+"\"")
	}
	return strings.Join(r, ",")
}
