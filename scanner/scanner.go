package scanner

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/valyala/fasthttp"
	"github.com/yanmengfei/poc-engine-xray/build"
	"github.com/yanmengfei/poc-engine-xray/library/decode"
	"github.com/yanmengfei/poc-engine-xray/library/http"
	"github.com/yanmengfei/poc-engine-xray/library/proto"
)

var scannerPool = sync.Pool{New: func() interface{} { return new(scanner) }}

type scanner struct {
	env *cel.Env
	set map[string]interface{}
}

func (s *scanner) Start(target string, rules []build.PocRule) (verify bool, err error) {
	for i := 0; i < len(rules); i++ {
		rules[i].DecodeSet(s.set)
		verify, err = s.scan(target, &rules[i])
		if request, ok := s.set["request"]; ok {
			// 回收 model request
			proto.ReleaseRequest(request.(*proto.Request))
		}
		if response, ok := s.set["response"]; ok {
			// 回收 model response
			proto.ReleaseResponse(response.(*proto.Response))
		}
		if err != nil {
			return verify, err
		}
		if !verify {
			return verify, nil
		}
	}
	return verify, nil
}

func (s *scanner) StartByGroups(target string, groups map[string][]build.PocRule) (verify bool, err error) {
	for _, rules := range groups {
		for i := 0; i < len(rules); i++ {
			if verify, err = s.Start(target, rules); err != nil {
				return verify, err
			}
			if verify {
				return verify, nil
			}
		}
	}
	return verify, nil
}

// scan 扫描逻辑
func (s *scanner) scan(target string, rule *build.PocRule) (bool, error) {
	urlpath := fmt.Sprintf("%s/%s",
		strings.TrimRight(target, "/"),
		strings.TrimLeft(rule.Path, "/"),
	)
	request, err := proto.SetupRequest(rule.Method, urlpath, rule.Body, rule.Headers)
	if err != nil {
		return false, err
	}
	s.set["request"] = request
	origin := request.ToFasthttp()
	response, err := http.Do(origin, rule.FollowRedirects)
	defer func() {
		response.SetConnectionClose()
		fasthttp.ReleaseResponse(response)
	}()
	if err != nil {
		return false, err
	}
	resp, err := proto.SetupResponse(response, origin)
	if err != nil {
		return false, err
	}
	if rule.Search != "" {
		if err = decode.Search(strings.TrimSpace(rule.Search), string(resp.Body), s.set); err != nil {
			return false, nil
		}
		return true, nil
	}
	s.set["response"] = resp
	out, err := decode.Evaluate(s.env, rule.Expression, s.set)
	if err != nil {
		return false, nil
	}
	return out.Value().(bool), nil
}

func New(poc *build.PocEvent) (scan *scanner, err error) {
	if poc == nil {
		return nil, errors.New("无效的Poc")
	}
	scan = scannerPool.Get().(*scanner)
	if scan.env, err = decode.NewCelEnv(poc.Set); err != nil {
		Release(scan)
		return nil, err
	}
	scan.set = poc.DecodeSet(scan.env)
	return scan, nil
}

func Release(scan *scanner) {
	scan.env = nil
	scan.set = nil
	scannerPool.Put(scan)
}
