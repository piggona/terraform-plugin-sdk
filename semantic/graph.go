package main

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/internal/configs/configload"
	"github.com/hashicorp/terraform-plugin-sdk/internal/providers"
	"github.com/hashicorp/terraform-plugin-sdk/semantic/lib/log"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
	"github.com/terraform-providers/terraform-provider-tencentcloud/tencentcloud"
)

const (
	walkInvalid byte = iota
	walkApply
	walkPlan
	walkPlanDestroy
	walkRefresh
	walkValidate
	walkDestroy
	walkImport
	walkEval // used just to prepare EvalContext for expression evaluation, with no other actions
)

type BrokerContext struct {
	terraform.Context
}

type Verticle struct {
	// 表示节点类型(data resource,resource)
	Type string `json: "type"`
	// 表示节点操作类型(create,update,delete等)
	Mode    string                       `json:"mode"`
	Addr    string                       `json:"addr"`
	Changes map[string]map[string]string `json:"changes"`
	To      []string                     `json:"to"`
}

type Graph struct {
	Verticles []*Verticle
	VMap      map[string]*Verticle
}

// func (c *BrokerContext) Check() (map[string]*Graph, tfdiags.Diagnostics) {
// 	diags := c.Validate()
// 	if diags.HasErrors() {
// 		return nil, diags
// 	}
// 	// graph, _ := c.Graph(terraform.GraphTypeValidate, nil)
// 	// 剥离graph中的有效部分到数据结构Graph中
// 	return nil, nil
// }

func main() {
	moduleDir := "/home/haohao/go/src/git.code.oa.com/yuanhaojin/semantic_checker/test/.terraform/modules"
	configPath := "/home/haohao/go/src/git.code.oa.com/yuanhaojin/semantic_checker/test"
	// 初始化contextOpts（主要初始化provider）
	provider, _ := tencentcloud.Provider().(terraform.ResourceProvider)
	ctxProviders := map[string]terraform.ResourceProviderFactory{
		"tencentcloud": terraform.ResourceProviderFactoryFixed(provider),
	}
	newProviders := make(map[string]providers.Factory)
	for k, pf := range ctxProviders {
		factory := pf
		newProviders[k] = func() (providers.Interface, error) {
			p, err := factory()
			if err != nil {
				log.Error("provider factory exec error: %s\n", err)
				return nil, err
			}
			return resource.GRPCTestProvider(p), nil
		}
	}
	providerResolver := providers.ResolverFixed(newProviders)
	opts := terraform.ContextOpts{ProviderResolver: providerResolver}
	// 配置contextOpts,生成context
	loader, err := configload.NewLoader(&configload.Config{
		ModulesDir: moduleDir,
	})
	if err != nil {
		return
	}
	cfg, diags := loader.LoadConfig(configPath)
	if diags.HasErrors() {
		log.Error(diags.Error())
		return
	}
	opts.Config = cfg
	// 此处加上导入state的逻辑
	opts.State, err = terraform.ShimLegacyState(&terraform.State{})
	if err != nil {
		log.Error("Shim Legacy State error: %s\n", err)
		return
	}
	ctx, stepDiags := terraform.NewContext(&opts)
	if stepDiags.HasErrors() {
		log.Error("Error initializing context: %s", stepDiags.Err())
		return
	}
	// 刷新state为用户当前state文件描述的状态
	_, refreshDiags := ctx.Refresh()
	if refreshDiags.HasErrors() {
		log.Error("Error refresh state: %s", refreshDiags.Err())
		return
	}
	ctx.AquireRun("plan")
	// 得到新的state与ctx.changes
	graph, _ := ctx.Graph(terraform.GraphTypePlan, nil)
	ctx.Walk(graph, terraform.NewOperation(walkPlan))

	g := &Graph{VMap: make(map[string]*Verticle)}
	resource_verticles := []*terraform.NodePlannableResource{}
	verticles := graph.AcyclicGraph.Vertices()
	// edges := graph.AcyclicGraph.Edges()
	for _, verticle := range verticles {
		switch verticle.(type) {
		case *terraform.NodePlannableResource:
			v := &Verticle{Changes: make(map[string]map[string]string)}
			resource := verticle.(*terraform.NodePlannableResource)
			resource_verticles = append(resource_verticles, resource)
			v.Addr = resource.Addr.String()
			v.Type = resource.Addr.Resource.Mode.String()
			for _, edge := range graph.AcyclicGraph.EdgesTo(verticle) {
				if tov, ok := edge.Source().(*terraform.NodePlannableResource); ok {
					v.To = append(v.To, tov.Addr.String())
				}
			}
			g.VMap[v.Addr] = v
			g.Verticles = append(g.Verticles, v)
		}
	}
	resourceChanges := ctx.GetChanges().Resources
	for _, change := range resourceChanges {
		resourceKey := change.Addr.Resource.Key
		addr := change.Addr.ShortString()

		// fmt.Println(before.GoString())
		// fmt.Println(after.GoString())
		if v, ok := g.VMap[addr]; ok {
			var rk string
			v.Mode = change.ChangeSrc.Action.String()
			if resourceKey == nil {
				rk = "0"
			} else {
				rk = resourceKey.String()
			}
			v.Changes[rk] = make(map[string]string)
			t, _ := change.ChangeSrc.After.ImpliedType()
			c, _ := change.ChangeSrc.Decode(t)
			before := c.Before
			after := c.After
			iter := after.ElementIterator()
			isnull := before.IsNull()
			for iter.Next() {
				key, value := iter.Element()
				if isnull || !value.Equals(before.GetAttr(key.AsString())).True() {
					v.Changes[rk][key.AsString()] = value.GoString()
				}
			}
		}
	}
	bytes, _ := json.Marshal(g.Verticles)
	fmt.Println(string(bytes))

}
