import { combineReducers } from 'redux';

import { authentication } from './authentication.reducer';
import { registration } from './registration.reducer';
import { subscriptions } from './subscriptions.reducer';

const rootReducer = combineReducers({
    authentication,
    registration,
    subscriptions,
});

export default rootReducer;
