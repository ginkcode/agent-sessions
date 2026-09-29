package bundle

import "github.com/ginkcode/agent-sessions/internal/model"

func sessionRef(agent, id string) model.SessionRef {
	return model.SessionRef{Agent: model.AgentID(agent), ID: id}
}
