import { createRouter, createWebHistory, type RouteRecordRaw, type RouterHistory } from 'vue-router'

// Titles are user-facing (Turkish). The campaign page tells its step from the campaign's server state, not from the URL
// alone, so a reload or a shared link opens where the campaign stands.
export const routes: RouteRecordRaw[] = [
  { path: '/', name: 'campaigns', component: () => import('./views/CampaignListView.vue'), meta: { title: 'Kampanyalar' } },
  { path: '/campaigns/new', name: 'campaign-new', component: () => import('./views/CampaignNewView.vue'), meta: { title: 'Yeni kampanya' } },
  {
    path: '/campaigns/:id(\\d+)',
    name: 'campaign',
    component: () => import('./views/CampaignView.vue'),
    props: (route) => ({ id: Number(route.params.id) }),
    meta: { title: 'Kampanya' },
  },
  { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue'), meta: { title: 'Sayfa bulunamadı' } },
]

export function createAppRouter(history: RouterHistory = createWebHistory()) {
  const router = createRouter({ history, routes })
  router.afterEach((to) => {
    const title = typeof to.meta.title === 'string' ? to.meta.title : ''
    document.title = title ? `${title} – Smart Campaign Composer` : 'Smart Campaign Composer'
  })
  return router
}

export const router = createAppRouter()
