import { combineReducers } from 'redux';
import auth from './authReducer.js';

const rootReducer = combineReducers({
  auth,
});

export default rootReducer;
