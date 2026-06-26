package provider

type StaticProvider struct {
	ProviderName    string
	RequestAdapter  ProviderAdapter
	AsyncAdapter    AsyncTaskAdapter
	CapabilitySet   ProviderCapabilities
	AuthStrategy    AuthStrategy
	ErrorTranslator ErrorTranslator
	MetaInfo        ProviderMeta
}

func (p *StaticProvider) Name() string {
	return p.ProviderName
}

func (p *StaticProvider) Request() ProviderAdapter {
	return p.RequestAdapter
}

func (p *StaticProvider) Async() AsyncTaskAdapter {
	return p.AsyncAdapter
}

func (p *StaticProvider) Capabilities() ProviderCapabilities {
	return p.CapabilitySet
}

func (p *StaticProvider) Auth() AuthStrategy {
	return p.AuthStrategy
}

func (p *StaticProvider) Errors() ErrorTranslator {
	return p.ErrorTranslator
}

func (p *StaticProvider) Meta() ProviderMeta {
	meta := p.MetaInfo
	if meta.Name == "" {
		meta.Name = p.ProviderName
	}
	if meta.Label == "" {
		meta.Label = p.ProviderName
	}
	if meta.ProtocolType == "" {
		meta.ProtocolType = "openai"
	}
	meta.Capabilities = p.CapabilitySet
	meta.SupportsAsync = p.AsyncAdapter != nil || p.CapabilitySet.AsyncTask
	if strategy, ok := p.AuthStrategy.(HeaderAuthStrategy); ok {
		if meta.AuthHeader == "" {
			meta.AuthHeader = strategy.Header
		}
		if meta.AuthPrefix == "" {
			meta.AuthPrefix = strategy.Prefix
		}
	}
	return meta
}
