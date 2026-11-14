import { createMemoryHistory } from 'vue-router'
import { describe, expect, it } from 'vitest'
import { createAppRouter } from './router'

function newRouter() {
  return createAppRouter(createMemoryHistory())
}

describe('routes', () => {
  it.each([
    ['/', 'campaigns'],
    ['/campaigns/new', 'campaign-new'],
    ['/campaigns/12', 'campaign'],
    ['/campaigns/abc', 'not-found'],
    ['/campaigns/12/extra', 'not-found'],
    ['/somewhere/else', 'not-found'],
  ])('%s opens %s', (path, name) => {
    expect(newRouter().resolve(path).name).toBe(name)
  })

  it('passes the campaign id to the page as a number', async () => {
    const router = newRouter()
    await router.push('/campaigns/42')
    const record = router.currentRoute.value.matched[0]
    const props = record?.props.default
    expect(typeof props).toBe('function')
    expect((props as (r: unknown) => unknown)(router.currentRoute.value)).toEqual({ id: 42 })
  })

  it('sets the document title from the route', async () => {
    const router = newRouter()
    await router.push('/campaigns/new')
    expect(document.title).toBe('Yeni kampanya – Smart Campaign Composer')
  })
})
