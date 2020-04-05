import React from 'react';
import { connect } from 'react-redux';
import PropTypes from 'prop-types';

import { getUser } from '../actions/user.action';
import { getSubscriptions } from '../actions/subscriptions.action';

import LoadingScreen from './LoadingScreen';

// eslint-disable-next-line react/prop-types
const InitialLoader = ({ auth, subscriptions, dispatch, ...rest }) => {
    let loaded = true;

    if (auth.notReady) {
        loaded = false;
        if (!auth.loading) {
            dispatch(getUser());
        }
    }

    if (subscriptions.loaded === false) {
        loaded = false;
        dispatch(getSubscriptions());
    }

    if (loaded === false) return <LoadingScreen />;

    return rest.children;
};

InitialLoader.propTypes = {
    auth: PropTypes.object.isRequired,
};

const mapStateToProps = state => ({
    auth: state.authentication,
    subscriptions: state.subscriptions,
});

export default connect(mapStateToProps)(InitialLoader);
