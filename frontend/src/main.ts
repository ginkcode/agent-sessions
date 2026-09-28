import './style.css'
import { mount } from 'svelte'
import App from './App.svelte'
import { api } from './lib/api'
import { setupLinkInterceptor } from './lib/navigation'

setupLinkInterceptor(api);

// Svelte 5 mount API
const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app
