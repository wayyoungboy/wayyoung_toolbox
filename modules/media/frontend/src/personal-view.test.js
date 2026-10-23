import {expect,it} from 'vitest';
import {offsetAfterRemoval,moveListWork} from './personal-view';

it('returns to a valid history page after its last entry is removed',()=>{
 expect(offsetAfterRemoval(48,49,1)).toBe(0);
 expect(offsetAfterRemoval(96,97,1)).toBe(48);
 expect(offsetAfterRemoval(48,100,1)).toBe(48);
 expect(offsetAfterRemoval(0,1,1)).toBe(0);
});
it('moves ordered items across page boundaries without dropping unseen items',()=>{
 const ids=Array.from({length:60},(_,i)=>`work-${i}`);
 const moved=moveListWork(ids,'work-48',-1);
 expect(moved[47]).toBe('work-48');expect(moved[48]).toBe('work-47');
 expect(moved).toHaveLength(60);expect(ids[48]).toBe('work-48');
 expect(moveListWork(ids,'work-0',-1)).toEqual(ids);
 expect(moveListWork(ids,'missing',1)).toEqual(ids);
});
