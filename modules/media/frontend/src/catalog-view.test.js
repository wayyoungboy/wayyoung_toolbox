import {describe,expect,it} from 'vitest';
import {catalogView,catalogLocation} from './catalog-view';
describe('catalog navigation state',()=>{
 it('restores a complete validated search and filter state',()=>{
  expect(catalogView({query:'mountain',actor_name:'Actor',tag:'travel',view:'continue',sort:'title',library:'2',list:'saved',offset:'96'})).toEqual({query:'mountain',actor:'Actor',tag:'travel',view:'continue',sort:'title',libraryID:'2',listID:'saved',offset:96});
  const state=catalogView({view:'invalid',sort:'drop',offset:'NaN'});expect(state.view).toBe('all');expect(state.sort).toBe('recent');expect(state.offset).toBe(0);
 });
 it('preserves the application URL and safe query encoding without storing credentials',()=>{
  const url=catalogLocation('http://localhost/app/media/?page=catalog&work=old',catalogView({query:'A&B',view:'favorites',offset:'48'}),'new');
  expect(url.pathname).toBe('/app/media/');expect(url.searchParams.get('query')).toBe('A&B');expect(url.searchParams.get('view')).toBe('favorites');expect(url.searchParams.get('work')).toBe('new');
  expect(catalogLocation(url,catalogView({}), '').searchParams.has('work')).toBe(false);
 });
});
